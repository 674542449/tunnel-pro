#!/usr/bin/env python3
"""Exercise the real desktop App through Wails' vendored native dispatcher.

Go overlay injects a test-only public adapter and test into the build graph;
neither the dependency tree nor the desktop release sources are modified.
No GUI, private account, real management service or system proxy is touched.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def main():
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default=shutil.which("go") or "C:/Program Files/Go/bin/go.exe")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    version = (root / "VERSION").read_text(encoding="utf-8").strip()
    output = args.output or root / "docs" / f"desktop-binding-{version}.json"
    stage_root = root / ".local" / "wails-binding"
    stage_root.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix="regression-", dir=stage_root))
    fixtures = root / "tools" / "desktop-binding"
    overlay = {
        "Replace": {
            str(root / "desktop/vendor/github.com/wailsapp/wails/v2/pkg/assetserver/tunnelx_binding_overlay.go"):
                str(fixtures / "assetserver_overlay.go.in"),
            str(root / "desktop/tunnelx_binding_overlay_test.go"):
                str(fixtures / "app_overlay_test.go.in"),
        }
    }
    overlay_path = stage / "overlay.json"
    overlay_path.write_text(json.dumps(overlay, indent=2), encoding="utf-8")
    command = [args.go, "test", "-overlay", str(overlay_path), "-tags=http2legacy", "-run", "^TestWailsBinding", "-count=1", "-json", "."]
    env = os.environ.copy()
    env.setdefault("GOCACHE", str(root / ".local" / "go-cache"))
    (stage / "state-fixtures").mkdir()
    env["TUNNELX_BINDING_STATE_ROOT"] = str(stage / "state-fixtures")
    result = subprocess.run(command, cwd=root / "desktop", env=env, text=True, encoding="utf-8", errors="replace", capture_output=True, timeout=240)
    (stage / "go-test.jsonl").write_text(result.stdout, encoding="utf-8")
    (stage / "go-test.stderr.txt").write_text(result.stderr, encoding="utf-8")
    cases, failures = [], []
    for line in result.stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("Action") == "pass" and event.get("Test"):
            cases.append(event["Test"])
        if event.get("Action") == "fail":
            failures.append(event.get("Test", event.get("Package", "unknown")))
        if result.returncode and event.get("Output"):
            print(event["Output"], end="")
    report = {
        "passed": result.returncode == 0 and len(cases) >= 19,
        "desktop_version": version,
        "checked_at": datetime.now(timezone.utc).isoformat(),
        "scope": "actual App.Query + desktop.Engine + vendored Wails Bindings, Dispatcher, ParseArgs, BoundMethod.Call and callback serializer",
        "gui_rendered": False,
        "remote_service_used": False,
        "system_proxy_modified": False,
        "injection": "go -overlay, test-only adapter; dependency and release sources unchanged",
        "source_hashes": {
            path: hashlib.sha256((root / path).read_bytes()).hexdigest()
            for path in [
                "desktop/main.go", "desktop/headless.go",
                "desktop/vendor/github.com/wailsapp/wails/v2/internal/binding/boundMethod.go",
                "desktop/vendor/github.com/wailsapp/wails/v2/internal/frontend/dispatcher/calls.go",
                "desktop/vendor/github.com/wailsapp/wails/v2/internal/frontend/dispatcher/dispatcher.go",
                "tools/desktop-binding/assetserver_overlay.go.in",
                "tools/desktop-binding/app_overlay_test.go.in",
            ]
        },
        "cases": cases,
        "failures": failures,
        "exit_code": result.returncode,
        "test_log": str(stage / "go-test.jsonl"),
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    if result.stderr:
        print(result.stderr, file=sys.stderr, end="")
    print(json.dumps({"passed": report["passed"], "cases": len(cases), "report": str(output)}, ensure_ascii=False))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
