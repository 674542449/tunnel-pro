"""One product version for the console and Windows desktop; core version is separate."""
import argparse
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parents[1]
NUMBER = r'[0-9]+\.[0-9]+\.[0-9]+'
# Only current product labels are generated. Never rewrite historical reports or core.Version.
TARGETS = {
    'desktop/wails.json': [(r'"productVersion": "(' + NUMBER + r')"', 1, False)],
    'internal/control/model.go': [(r'const ConsoleVersion = "(v' + NUMBER + r')"', 1, True)],
    'internal/desktop/engine.go': [(r'const DesktopVersion = "(v' + NUMBER + r')"', 1, True)],
    'internal/control/web/index.html': [(r'\bv(' + NUMBER + r')\b', 2, False), (r'\?v=(' + NUMBER + r')', 2, False)],
    'desktop/ui/index.html': [(r'\bv(' + NUMBER + r')\b', 2, False)],
    'desktop/ui/app.js': [(r"status\.version \|\| '(v" + NUMBER + r")'", 3, True)],
}


def read_version(root=ROOT):
    value = (root / 'VERSION').read_text(encoding='utf-8').strip()
    if not re.fullmatch('v' + NUMBER, value):
        raise ValueError('VERSION must contain vMAJOR.MINOR.PATCH')
    return value


def check(root=ROOT, expected=None):
    version = read_version(root)
    if expected is not None and expected != version:
        raise ValueError('Requested version differs from VERSION')
    for name, rules in TARGETS.items():
        data = (root / name).read_text(encoding='utf-8')
        for pattern, count, prefixed in rules:
            matches = list(re.finditer(pattern, data))
            wanted = version if prefixed else version[1:]
            if len(matches) != count or any(m[1] != wanted for m in matches):
                raise ValueError('Version drift in ' + name + '; run tools/versioning.py --sync')
    return version


def sync(root=ROOT):
    version = read_version(root)
    edits = {}
    for name, rules in TARGETS.items():
        data = (root / name).read_text(encoding='utf-8')
        for pattern, count, prefixed in rules:
            matches = list(re.finditer(pattern, data))
            if len(matches) != count:
                raise ValueError('Unexpected version labels in ' + name)
            for match in reversed(matches):
                data = data[:match.start(1)] + (version if prefixed else version[1:]) + data[match.end(1):]
        edits[name] = data
    for name, data in edits.items():
        (root / name).write_text(data, encoding='utf-8', newline='\n')
    return check(root)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sync', action='store_true', help='Generate current product labels from VERSION')
    parser.add_argument('--expect', help='Reject a build/package argument different from VERSION')
    args = parser.parse_args()
    try:
        if args.sync:
            sync()
        print(check(expected=args.expect))
    except (ValueError, OSError) as error:
        parser.exit(1, str(error) + '\n')
