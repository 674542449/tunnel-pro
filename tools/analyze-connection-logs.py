"""Summarize local metadata logs; EOF is reported separately from path failures."""
import argparse
import collections
import datetime
import json
import pathlib

p=argparse.ArgumentParser()
p.add_argument('--log-dir',required=True)
p.add_argument('--since',help='ISO time, or local Shanghai YYYY-MM-DD HH:MM:SS')
p.add_argument('--until')
p.add_argument('--app',help='Windows process basename, e.g. steam.exe')
p.add_argument('--out',help='Optional summary JSON output; never write this into a release package')
args=p.parse_args()
shanghai=datetime.timezone(datetime.timedelta(hours=8))
def parse_time(value):
    if value is None:return None
    d=datetime.datetime.fromisoformat(value.replace('Z','+00:00'))
    return d if d.tzinfo else d.replace(tzinfo=shanghai)
start,end=parse_time(args.since),parse_time(args.until)
events=[];invalid=0;incomplete=0;sessions={}
for file in sorted(pathlib.Path(args.log_dir).glob('events-*.jsonl')):
    data=file.read_bytes()
    lines=data.splitlines(keepends=True)
    for line in lines:
        if not line.endswith(b'\n'):incomplete+=1;continue
        try:
            event=json.loads(line);time=parse_time(event['time'])
        except (ValueError,KeyError,TypeError):invalid+=1;continue
        if start and time<start or end and time>end:continue
        event['local_time']=time.astimezone(shanghai).isoformat()
        events.append(event)
events.sort(key=lambda e:e['time'])
all_events=events
if args.app:
    events=[e for e in events if e.get('application',{}).get('process_name','').casefold()==args.app.casefold()]
counts=collections.Counter(e['event'] for e in events)
apps={}
non_path_kinds={'cancelled','socket_closed','pipe_closed','eof','none','normal_quic_close','stream_cancelled'}
def compact(e):
    return {k:e[k] for k in ('local_time','event','scope','connection_id','carrier_id','transport','from','to','target','application','stage','failure_stage','probe_started_at','error_kind','os_error_code','quic_error_code','h2_error_code','remote','http_status','carrier_failure_confirmed','observed_failure_window_ms','reason','direction','termination') if k in e}
for e in events:
    app=e.get('application')
    if not app:continue
    name=app.get('process_name') or 'unidentified'
    a=apps.setdefault(name,dict(requested=0,opened=0,ended=0,fallbacks=0,error_events=0,target_eof_events=0,pids=set(),targets=set()))
    if app.get('pid'):a['pids'].add(app['pid'])
    if e.get('target'):a['targets'].add(e['target'])
    if e['event']=='connection_requested':a['requested']+=1
    if e['event']=='connection_opened':a['opened']+=1
    if e['event']=='connection_ended':a['ended']+=1
    if e['event']=='transport_fallback':a['fallbacks']+=1
    if e['event']=='connection_error' and e.get('error_kind') not in non_path_kinds:a['error_events']+=1
    if e['event']=='stream_direction_ended' and e.get('termination')=='target_eof':a['target_eof_events']+=1
for a in apps.values():a['pids']=sorted(a['pids']);a['targets']=sorted(a['targets'])[:20]
for e in all_events:
    s=sessions.setdefault(e['session'],dict(first=e['local_time'],last=e['local_time'],dropped_events=0,io_errors=0,stopped=False))
    s['last']=e['local_time']
    if e['event']=='client_stopped':s['stopped']=True
    logging=e.get('logging',{})
    s['dropped_events']=max(s['dropped_events'],logging.get('dropped_events',0))
    s['io_errors']=max(s['io_errors'],logging.get('io_errors',0))
report=dict(checked_at=datetime.datetime.now(shanghai).isoformat(),log_directory=str(pathlib.Path(args.log_dir).resolve()),
    requested_since=start.isoformat() if start else None,requested_until=end.isoformat() if end else None,
    first_record=events[0]['local_time'] if events else None,last_record=events[-1]['local_time'] if events else None,
    event_counts=dict(counts),applications=apps,sessions=sessions,invalid_lines=invalid,incomplete_last_lines=incomplete,
    path_probe_failures=[compact(e) for e in events if e['event']=='path_probe_failed'][-100:],
    path_probe_recoveries=[compact(e) for e in events if e['event']=='path_probe_recovered'][-100:],
    transport_fallbacks=[compact(e) for e in events if e['event']=='transport_fallback'][-100:],
    carrier_failures=[compact(e) for e in events if e['event']=='carrier_failed'][-100:],
    application_error_examples=[compact(e) for e in events if e['event']=='connection_error' and e.get('error_kind') not in non_path_kinds][-100:],
    target_eof_examples=[compact(e) for e in events if e['event']=='stream_direction_ended' and e.get('termination')=='target_eof'][-30:],
    interpretation='Target EOF is an observed close, not proof of an outage. Probes are sampled every 30 seconds by default; failures do not establish ISP QoS. Only traffic traversing this proxy can be attributed.')
output=json.dumps(report,indent=2,ensure_ascii=False)+'\n'
if args.out:pathlib.Path(args.out).write_text(output,encoding='utf-8')
print(output,end='')
