#!/usr/bin/env python3
"""Seed the development SonarQube with sample projects and analysis history.

Runs inside the sonar-scanner-cli image (Python, curl and the scanner are all
there). For each sample project it generates small Python sources with a known
number of bugs, vulnerabilities, code smells and covered lines, and analyses
them several times with backdated dates, so the server has measure history,
version events and quality gate results to query.

It then writes a user token for Grafana and the tests to $SEED_OUT/token.

Idempotent: projects that already have their analyses are left alone, and the
token is kept while it is still valid. Everything here is for local
development; the admin password and token are throwaway.
"""
import base64
import datetime as dt
import json
import os
import pathlib
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

URL = os.environ.get("SONAR_URL", "http://sonarqube:9000").rstrip("/")
ADMIN_USER = os.environ.get("SONAR_ADMIN_USER", "admin")
ADMIN_PASSWORD = os.environ.get("SONAR_ADMIN_PASSWORD", "admin")
OUT = pathlib.Path(os.environ.get("SEED_OUT", "/seed-out"))
TOKEN_NAME = "grafana-dev"
# Set by wait_for_server: servers before 10.0 take the scanner's token as
# sonar.login, later ones as sonar.token.
TOKEN_PROPERTY = "sonar.token"

# (days ago, version, bugs, vulnerabilities, code smells, hotspots, covered %, duplicated blocks)
PROJECTS = {
    "demo:shop-api": {
        "name": "Shop API",
        "tags": ["backend", "shop"],
        "runs": [
            (84, "1.0", 6, 2, 9, 2, 35, 2),
            (63, "1.1", 5, 2, 8, 2, 48, 2),
            (42, "1.2", 7, 1, 8, 1, 55, 1),
            (28, "2.0", 3, 1, 6, 1, 68, 1),
            (14, "2.1", 2, 0, 5, 1, 76, 0),
            (3, "2.2", 1, 0, 4, 0, 84, 0),
        ],
    },
    "demo:shop-web": {
        "name": "Shop Web",
        "tags": ["frontend", "shop"],
        "runs": [
            (70, "0.8", 2, 0, 12, 1, 60, 3),
            (35, "0.9", 3, 1, 14, 1, 58, 3),
            (21, "1.0", 3, 1, 11, 2, 61, 2),
            (2, "1.1", 4, 2, 13, 2, 52, 2),
        ],
    },
    "demo:legacy-billing": {
        "name": "Legacy Billing",
        "tags": ["backend", "legacy"],
        "review_hotspot": True,
        "runs": [
            (56, "7.3", 9, 3, 15, 3, 12, 4),
            (7, "7.4", 9, 4, 16, 3, 10, 4),
        ],
    },
}


def log(msg):
    print(f"[seed] {msg}", flush=True)


def api(path, params=None, method="GET", token=None, user=None, password=None, ok=(200, 204)):
    data = None
    url = f"{URL}/{path}"
    if params:
        encoded = urllib.parse.urlencode(params)
        if method == "GET":
            url += "?" + encoded
        else:
            data = encoded.encode()
    req = urllib.request.Request(url, data=data, method=method)
    if token:
        cred = f"{token}:"
    else:
        cred = f"{user or ADMIN_USER}:{password or ADMIN_PASSWORD}"
    req.add_header("Authorization", "Basic " + base64.b64encode(cred.encode()).decode())
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            body = resp.read().decode()
            return resp.status, (json.loads(body) if body.strip().startswith(("{", "[")) else body)
    except urllib.error.HTTPError as e:
        body = e.read().decode()
        if e.code in ok:
            return e.code, body
        return e.code, body


def wait_for_server():
    for _ in range(180):
        try:
            with urllib.request.urlopen(f"{URL}/api/system/status", timeout=5) as resp:
                status = json.load(resp)
                if status.get("status") == "UP":
                    global TOKEN_PROPERTY
                    version = status.get("version", "")
                    major = int(version.split(".")[0]) if version.split(".")[0].isdigit() else 99
                    if major < 10:
                        TOKEN_PROPERTY = "sonar.login"
                    log(f"SonarQube {version} is up")
                    return
        except Exception:
            pass
        time.sleep(2)
    sys.exit("SonarQube did not come up")


def ensure_token():
    OUT.mkdir(parents=True, exist_ok=True)
    token_file = OUT / "token"
    if token_file.exists():
        token = token_file.read_text().strip()
        code, body = api("api/authentication/validate", token=token)
        if code == 200 and isinstance(body, dict) and body.get("valid"):
            log("existing token is still valid")
            return token
    api("api/user_tokens/revoke", {"name": TOKEN_NAME}, method="POST")
    code, body = api("api/user_tokens/generate", {"name": TOKEN_NAME}, method="POST")
    if code != 200:
        sys.exit(f"could not create a token: {code} {body}")
    token = body["token"]
    token_file.write_text(token)
    token_file.chmod(0o644)
    log("wrote a new token")
    return token


def analysis_count(key):
    code, body = api("api/project_analyses/search", {"project": key, "ps": 100})
    if code != 200:
        return 0
    return len(body.get("analyses", []))


SMELL = '''def helper_{i}(a, b, c):
    unused_{i} = a + b
    return c


def placeholder_{i}():
    pass
'''
BUG = '''def check_{i}(value):
    if value == value:
        return {i}
    return 0
'''
VULN = '''def fetch_{i}(url):
    return requests.get(url, verify=False)
'''
# A hard-coded IP address is a security hotspot on SonarQube 9.9. Community
# Build 26.11 has no hotspot rules at all (the former ones, such as weak
# hashing, are vulnerabilities there), so this adds nothing on it.
HOTSPOT = '''def connect_{i}():
    host = "192.168.10.{i}"
    return hashlib.sha256(host.encode()).hexdigest()
'''
DUP = '''def report_block_{i}(rows):
    total = 0
    count = 0
    for row in rows:
        if row is None:
            continue
        total = total + row.amount
        count = count + 1
        if count > 1000:
            break
    average = total / max(count, 1)
    lines = []
    lines.append("total: %s" % total)
    lines.append("count: %s" % count)
    lines.append("average: %s" % average)
    return lines
'''


def write_project(root, bugs, vulns, smells, hotspots, covered, dups):
    if root.exists():
        shutil.rmtree(root)
    src = root / "src"
    src.mkdir(parents=True)
    files = {
        "checks.py": "".join(BUG.format(i=i) for i in range(bugs)) or "VALUE = 1\n",
        "client.py": "import hashlib\nimport requests\n\n"
        + "".join(VULN.format(i=i) for i in range(vulns))
        + "".join(HOTSPOT.format(i=i) for i in range(hotspots))
        + "def ping():\n    return requests.__name__ + hashlib.__name__\n",
        "helpers.py": "".join(SMELL.format(i=i) for i in range(smells)) or "VALUE = 2\n",
        "reports.py": "".join(DUP.format(i=i) for i in range(dups + 1)),
    }
    coverage = ['<coverage version="1">']
    for name, text in files.items():
        (src / name).write_text(text)
        lines = text.count("\n")
        coverage.append(f'  <file path="src/{name}">')
        for n in range(1, lines + 1):
            hit = "true" if (n * 100 // max(lines, 1)) <= covered else "false"
            coverage.append(f'    <lineToCover lineNumber="{n}" covered="{hit}"/>')
        coverage.append("  </file>")
    coverage.append("</coverage>")
    (root / "coverage.xml").write_text("\n".join(coverage) + "\n")


def scan_once(key, name, version, date, root, token):
    """Run one analysis and wait for the server to process it. Returns the
    final Compute Engine task status."""
    cmd = [
        "sonar-scanner",
        f"-Dsonar.host.url={URL}",
        f"-D{TOKEN_PROPERTY}={token}",
        f"-Dsonar.projectKey={key}",
        f"-Dsonar.projectName={name}",
        f"-Dsonar.projectVersion={version}",
        f"-Dsonar.projectDate={date}",
        "-Dsonar.sources=src",
        "-Dsonar.coverageReportPaths=coverage.xml",
        "-Dsonar.python.version=3.12",
        "-Dsonar.scm.disabled=true",
        "-Dsonar.qualitygate.wait=false",
    ]
    run = subprocess.run(cmd, cwd=root, capture_output=True, text=True)
    if run.returncode != 0:
        print(run.stdout[-3000:], run.stderr[-2000:])
        sys.exit(f"scan of {key} {version} failed")
    task_id = None
    for line in (root / ".scannerwork" / "report-task.txt").read_text().splitlines():
        if line.startswith("ceTaskId="):
            task_id = line.split("=", 1)[1]
    for _ in range(150):
        code, body = api("api/ce/task", {"id": task_id})
        status = body.get("task", {}).get("status") if code == 200 else None
        if status in ("SUCCESS", "FAILED", "CANCELED"):
            return status
        time.sleep(2)
    return "TIMEOUT"


def scan(key, name, version, date, root, token):
    # A failed report records no analysis, so the same date can be submitted
    # again; one retry covers a server that is still starting its workers.
    for attempt in range(1, 3):
        status = scan_once(key, name, version, date, root, token)
        if status == "SUCCESS":
            return
        log(f"{key} {version}: server-side processing {status}, attempt {attempt}; retrying")
        time.sleep(5 * attempt)
    sys.exit(f"{key} {version} could not be analysed")


def keep_closed_issues():
    # SonarQube deletes issues 30 days after they are closed. A backdated
    # analysis that closes an issue "more than 30 days ago" has it deleted in
    # the same run, and the server's issue indexer then fails the whole report
    # (NoSuchElementException in IssueIndexer, Community Build 26.11). Keep
    # closed issues for longer than the seeded history goes back.
    code, body = api(
        "api/settings/set",
        {"key": "sonar.dbcleaner.daysBeforeDeletingClosedIssues", "value": "3650"},
        method="POST",
    )
    if code not in (200, 204):
        sys.exit(f"could not change the closed-issue retention: {code} {body}")


def review_one_hotspot(key):
    """Mark the project's first hotspot as reviewed and safe, so there is
    something to see under each review status."""
    # The project parameter is "projectKey" before Server 10.4 and "project"
    # after; each version ignores the other name.
    code, body = api("api/hotspots/search", {"projectKey": key, "project": key, "ps": 100})
    hotspots = body.get("hotspots", []) if code == 200 else []
    if not hotspots:
        return
    first = sorted(hotspots, key=lambda h: (h.get("line", 0), h["key"]))[0]
    api(
        "api/hotspots/change_status",
        {"hotspot": first["key"], "status": "REVIEWED", "resolution": "SAFE"},
        method="POST",
    )
    log(f"{key}: marked one of {len(hotspots)} hotspots as safe")


def main():
    wait_for_server()
    token = ensure_token()
    keep_closed_issues()
    now = dt.datetime.now(dt.timezone.utc)
    work = pathlib.Path("/tmp/seed-work")
    for key, spec in PROJECTS.items():
        have = analysis_count(key)
        if have >= len(spec["runs"]):
            log(f"{key}: already has {have} analyses")
            continue
        if have:
            # A partial history cannot be backfilled (dates must increase).
            api("api/projects/delete", {"project": key}, method="POST")
        root = work / key.replace(":", "_")
        for days_ago, version, bugs, vulns, smells, hotspots, covered, dups in spec["runs"]:
            date = (now - dt.timedelta(days=days_ago)).strftime("%Y-%m-%dT%H:%M:%S+0000")
            write_project(root, bugs, vulns, smells, hotspots, covered, dups)
            started = time.time()
            scan(key, spec["name"], version, date, root, token)
            log(f"{key} {version} ({days_ago}d ago) analysed in {time.time() - started:.0f}s")
        api("api/project_tags/set", {"project": key, "tags": ",".join(spec["tags"])}, method="POST")
        if spec.get("review_hotspot"):
            review_one_hotspot(key)
    (OUT / "ready").write_text(now.isoformat())
    log("done")


if __name__ == "__main__":
    main()
