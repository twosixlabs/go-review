#!/usr/bin/env bash
# Purpose: run the isolated x/tools-vs-Argot controlled flow-reuse evaluation.
# Usage: scripts/evaluate-flow-reuse.sh [--candidate all|x-tools|argot] [--timeout-seconds N]
# Logs and /usr/bin/time -l measurements are written below .work/flow-reuse-evaluation/results/.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
python3 - "$repo_root" "$@" <<'PY'
import argparse
import datetime
import os
import pathlib
import shlex
import signal
import subprocess
import sys
import time

root = pathlib.Path(sys.argv[1])
parser = argparse.ArgumentParser(description="Run the bounded flow-reuse evaluation.")
parser.add_argument("--candidate", choices=("all", "x-tools", "argot", "argot-x42"), default="all")
parser.add_argument("--timeout-seconds", type=int, default=420)
args = parser.parse_args(sys.argv[2:])
if args.timeout_seconds < 1 or args.timeout_seconds > 840:
    parser.error("--timeout-seconds must be between 1 and 840")

time_bin = pathlib.Path("/usr/bin/time")
if not time_bin.exists():
    raise SystemExit("FAIL: /usr/bin/time is required for resource measurements")

run_id = datetime.datetime.now().strftime("%Y%m%dT%H%M%S")
out_dir = root / ".work" / "flow-reuse-evaluation" / "results" / run_id
out_dir.mkdir(parents=True, exist_ok=False)
fixture = root / ".work" / "flow-reuse-evaluation" / "fixture" / "app"
candidates = ("x-tools", "argot", "argot-x42") if args.candidate == "all" else (args.candidate,)
deadline = time.monotonic() + 14 * 60
failures = 0

print(f"run_id={run_id}")
print(f"fixture={fixture}")
print(f"logs={out_dir}")
print(f"go={subprocess.check_output(['go', 'version'], text=True).strip()}")

for candidate in candidates:
    module_name = "argot" if candidate == "argot-x42" else candidate
    module_dir = root / ".work" / "flow-reuse-evaluation" / module_name
    log_path = out_dir / f"{candidate}.log"
    go_args = ["run", "-modfile=go-x42.mod", "."] if candidate == "argot-x42" else ["run", "."]
    command = [str(time_bin), "-l", "go", *go_args]
    env = os.environ.copy()
    env["FLOW_FIXTURE"] = str(fixture)
    remaining = max(1, int(deadline - time.monotonic()))
    timeout = min(args.timeout_seconds, remaining)
    started = time.monotonic()
    print(f"\n[{candidate}] cwd={module_dir}")
    print(f"[{candidate}] command=FLOW_FIXTURE={shlex.quote(str(fixture))} "
          f"{shlex.join(command)}")
    print(f"[{candidate}] timeout={timeout}s log={log_path}")
    status = "PASS"
    exit_code = 0
    with log_path.open("w", encoding="utf-8") as log:
        try:
            process = subprocess.Popen(
                command,
                cwd=module_dir,
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            try:
                exit_code = process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                status = "TIMEOUT"
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()
                exit_code = 124
        except OSError as error:
            status = "FAIL"
            exit_code = 127
            log.write(f"launcher error: {error}\n")
    elapsed = time.monotonic() - started
    if exit_code != 0 and status == "PASS":
        status = "FAIL"
    if status != "PASS":
        failures += 1
    print(f"[{candidate}] result={status} exit={exit_code} elapsed={elapsed:.2f}s")
    print(f"[{candidate}] full diagnostic/resource log: {log_path}")

summary = out_dir / "summary.txt"
summary.write_text(
    f"run_id={run_id}\ncandidate={args.candidate}\n"
    f"fixture={fixture}\nfailures={failures}\n",
    encoding="utf-8",
)
print(f"\nsummary={summary}")
raise SystemExit(1 if failures else 0)
PY
