"""Vendor pinned MIT SVG country flags; no runtime CDN or executable upstream code."""
import hashlib, io, json, pathlib, re, urllib.request, xml.etree.ElementTree as ET, zipfile

root=pathlib.Path(__file__).resolve().parents[1]
commit='fe15c16e7463d0c66d6c5730e9d0e832438d98e1'
url='https://codeload.github.com/lipis/flag-icons/zip/'+commit
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with opener.open(url,timeout=45) as response: archive=response.read(32*1024*1024)
target=root/'desktop/ui/flags'; target.mkdir(parents=True,exist_ok=True)
with zipfile.ZipFile(io.BytesIO(archive)) as z:
 prefix='flag-icons-'+commit+'/'
 countries=json.loads(z.read(prefix+'country.json'))
 selected=[c for c in countries if c.get('iso') and re.fullmatch('[a-z]{2}',c['code'])]
 assert len(selected)==249
 hashes={}
 for c in selected:
  name=c['code']+'.svg';data=z.read(prefix+'flags/4x3/'+name)
  svg=ET.fromstring(data);assert svg.tag=='{http://www.w3.org/2000/svg}svg'
  for element in svg.iter():
   assert element.tag.rsplit('}',1)[-1] not in {'script','foreignObject','image','style','animate','set'}
   for key,value in element.attrib.items():
    key=key.rsplit('}',1)[-1].lower()
    assert not key.startswith('on')
    if key=='href':assert value.startswith('#')
    if 'url(' in value:assert re.fullmatch(r'url\(#[A-Za-z0-9_-]+\)',value)
  (target/name).write_bytes(data);hashes[name]=hashlib.sha256(data).hexdigest()
 license=z.read(prefix+'LICENSE')
 (target/'LICENSE.txt').write_bytes(license)
 (root/'desktop/runtime/flag-icons-LICENSE.txt').write_bytes(license)
manifest={'source':'https://github.com/lipis/flag-icons','version':'7.3.2','commit':commit,'archive_sha256':hashlib.sha256(archive).hexdigest(),'files':hashes}
(target/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n',encoding='utf-8')
print(json.dumps({'vendored_flags':len(hashes),'commit':commit,'codes':' '.join(sorted(n[:2].upper() for n in hashes))}))
