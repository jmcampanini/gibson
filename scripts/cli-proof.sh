#!/bin/sh
# Compiled-binary CLI proof: exercises the gibson binary end to end against
# fakepi in a disposable sandbox. Invoked by `make cli-proof` after a build;
# runnable standalone from the repository root once build/gibson exists.
set -eu

repo_root="$PWD"
gibson="$repo_root/${BINARY:-build/gibson}"
sandbox="$repo_root/.sandbox/cli-proof-$$"
trap 'rm -rf "$sandbox"' EXIT
test -x "$gibson"

root_help="$($gibson --help)"
printf '%s\n' "$root_help" | grep -Eq '^[[:space:]]+serve[[:space:]]'
printf '%s\n' "$root_help" | grep -Eq '^[[:space:]]+run[[:space:]]'
version="$($gibson --version)"
test "$version" = "gibson version cli-proof"
serve_help="$($gibson serve --help)"
printf '%s\n' "$serve_help" | grep -Fq -- '--port'
printf '%s\n' "$serve_help" | grep -Fq -- '--dev'
run_help="$($gibson run --help)"
printf '%s\n' "$run_help" | grep -Fq -- 'run <type> <message> [--checkout <name>]'
printf '%s\n' "$run_help" | grep -Fq -- '--checkout'
completion_fish="$($gibson completion fish)"
test -n "$completion_fish"

if error_output="$($gibson serve unexpected 2>&1)"; then
	echo "expected positional arguments to fail" >&2
	exit 1
else
	error_rc=$?
fi
test "$error_rc" -eq 1
test "$(printf '%s\n' "$error_output" | wc -l | tr -d ' ')" -eq 1
case "$error_output" in 'gibson: error: '*) ;; *) echo "missing process error prefix" >&2; exit 1;; esac

mkdir -p "$sandbox/main"
go build -o "$sandbox/pi" ./internal/fakepi
git -C "$sandbox/main" init -b main -q
printf '[server]\nport = 7311\npi_bin = "%s"\n\n[sessions.quick]\ndescription = "CLI proof"\n' "$sandbox/pi" > "$sandbox/main/gibson.toml"
printf '.gibson/\n' > "$sandbox/main/.gitignore"
git -C "$sandbox/main" add gibson.toml .gitignore
git -C "$sandbox/main" -c user.name='Gibson CLI Proof' -c user.email=gibson@example.invalid commit -qm init
cd "$sandbox/main"

run_stdout="$($gibson run quick 'Say hello' 2>"$sandbox/run.stderr")"
test "$run_stdout" = 'Hello from fake pi.'
grep -Fq '[session] id=' "$sandbox/run.stderr"
grep -Fq 'status=stopped' "$sandbox/run.stderr"
test "$(find .gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')" -eq 1
test "$(find .gibson/logs -type f -name '*.stderr.log' | wc -l | tr -d ' ')" -eq 1
grep -Fq '"version": 1' .gibson/state.json
grep -Fq '"status": "stopped"' .gibson/state.json
grep -Fq '"pid": 0' .gibson/state.json
test -z "$(git status --porcelain)"

if unknown_output="$($gibson run missing hi 2>&1)"; then
	echo "expected unknown session type to fail" >&2
	exit 1
else
	unknown_rc=$?
fi
test "$unknown_rc" -eq 1
test "$(printf '%s\n' "$unknown_output" | wc -l | tr -d ' ')" -eq 1
case "$unknown_output" in 'gibson: error: '*'configured types: quick'*) ;; *) echo "unknown type error did not list configured types" >&2; exit 1;; esac

git worktree add ../wt-x -b wt-x -q
main_sessions_before="$(find .gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')"
named_stdout="$($gibson run quick 'Named checkout' --checkout wt-x 2>"$sandbox/named.stderr")"
test "$named_stdout" = 'Hello from fake pi.'
test "$main_sessions_before" = "$(find .gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')"
test "$(find ../wt-x/.gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')" -eq 1
test "$(find ../wt-x/.gibson/logs -type f -name '*.stderr.log' | wc -l | tr -d ' ')" -eq 1
python3 -c "import glob,json,os; p=glob.glob('../wt-x/.gibson/sessions/*.jsonl'); assert len(p)==1; h=json.loads(open(p[0]).readline()); d=json.load(open('../wt-x/.gibson/state.json')); assert h['id'] in d['sessions']; assert h['cwd']==os.path.realpath('../wt-x'); assert d['sessions'][h['id']]['status']=='stopped' and d['sessions'][h['id']]['pid']==0"

mkdir "$sandbox/not-git"
for invalid in '../outside' missing not-git; do
	before_main="$(find .gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')"
	before_target="$(find ../wt-x/.gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')"
	if invalid_output="$($gibson run quick invalid --checkout "$invalid" 2>&1)"; then echo "expected checkout $invalid to fail" >&2; exit 1; else invalid_rc=$?; fi
	test "$invalid_rc" -eq 1
	case "$invalid_output" in 'gibson: error: '*) ;; *) echo "invalid checkout error missing process prefix" >&2; exit 1;; esac
	test "$before_main" = "$(find .gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')"
	test "$before_target" = "$(find ../wt-x/.gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')"
done

cp -R .gibson "$sandbox/rejected-main-gibson"
cp -R ../wt-x/.gibson "$sandbox/rejected-wt-x-gibson"
rejected_main_head="$(git rev-parse HEAD)"
rejected_wt_x_head="$(git -C ../wt-x rev-parse HEAD)"
if $gibson run quick rejected extra >"$sandbox/rejected.output" 2>&1; then
	echo "expected extra run operand to fail" >&2
	exit 1
else
	rejected_rc=$?
fi
test "$rejected_rc" -eq 1
test "$(wc -l <"$sandbox/rejected.output" | tr -d ' ')" -eq 1
test "$(awk 'END { print NR }' "$sandbox/rejected.output")" -eq 1
IFS= read -r rejected_output <"$sandbox/rejected.output"
case "$rejected_output" in 'gibson: error: '*) ;; *) echo "rejected operand error missing process prefix" >&2; exit 1;; esac
diff -r "$sandbox/rejected-main-gibson" .gibson
diff -r "$sandbox/rejected-wt-x-gibson" ../wt-x/.gibson
rejected_main_status="$(git status --porcelain)" || { echo "main checkout status failed" >&2; exit 1; }
rejected_wt_x_status="$(git -C ../wt-x status --porcelain)" || { echo "wt-x checkout status failed" >&2; exit 1; }
test -z "$rejected_main_status"
test -z "$rejected_wt_x_status"
test "$rejected_main_head" = "$(git rev-parse HEAD)"
test "$rejected_wt_x_head" = "$(git -C ../wt-x rev-parse HEAD)"
printf '%s\n' 'REJECTED_OPERAND_STATE=unchanged'

$gibson run quick 'Concurrent A' --checkout wt-x >"$sandbox/concurrent-a.stdout" 2>"$sandbox/concurrent-a.stderr" & concurrent_a=$!
$gibson run quick 'Concurrent B' --checkout wt-x >"$sandbox/concurrent-b.stdout" 2>"$sandbox/concurrent-b.stderr" & concurrent_b=$!
wait "$concurrent_a"; wait "$concurrent_b"
python3 -c "import json; d=json.load(open('../wt-x/.gibson/state.json')); assert len(d['sessions'])==3; assert all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values())"
test "$(find ../wt-x/.gibson/sessions -type f -name '*.jsonl' | wc -l | tr -d ' ')" -eq 3
python3 -c "import glob,os,stat; roots=['.gibson','../wt-x/.gibson']; assert all(stat.S_IMODE(os.stat(p).st_mode)==0o755 for r in roots for p in (r,r+'/sessions',r+'/logs')); assert all(stat.S_IMODE(os.stat(p).st_mode)==0o644 for r in roots for p in [r+'/state.json']+glob.glob(r+'/logs/*.stderr.log'))"
test -z "$(git status --porcelain)"
test -z "$(git -C ../wt-x status --porcelain)"
printf '%s\n' 'NAMED_CHECKOUT=wt-x' 'NAMED_CHECKOUT_ARTIFACT_ISOLATION=true' 'INVALID_CHECKOUTS_REJECTED=traversal,missing,non-git' 'CONCURRENT_REGISTRY_RECORDS=preserved' 'STORAGE_PERMISSIONS=0755,0644' 'CHECKOUT_GIT_STATUS=clean'

FAKEPI_SCENARIO=slow_stream python3 -c 'import os, sys; os.setpgid(0, 0); os.execv(sys.argv[1], sys.argv[1:])' "$gibson" run quick 'Stream until interrupted' >"$sandbox/interrupt.stdout" 2>"$sandbox/interrupt.stderr" &
interrupt_pid=$!
for _ in $(seq 1 100); do
	if test -s "$sandbox/interrupt.stdout"; then break; fi
	kill -0 "$interrupt_pid" 2>/dev/null || { echo "interrupt proof exited before streaming" >&2; exit 1; }
	sleep 0.02
done
test -s "$sandbox/interrupt.stdout"
python3 -c 'import os, signal, sys; os.killpg(int(sys.argv[1]), signal.SIGINT)' "$interrupt_pid"
if wait "$interrupt_pid"; then interrupt_rc=0; else interrupt_rc=$?; fi
test "$interrupt_rc" -eq 130
grep -Fq '"stopReason":"aborted"' .gibson/sessions/*.jsonl
python3 -c "import json; d=json.load(open('.gibson/state.json')); assert all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values())"
if pgrep -f "$PWD/.gibson/sessions" >/dev/null; then
	echo "first interrupt left an orphan" >&2; exit 1
else
	pgrep_rc=$?; test "$pgrep_rc" -eq 1 || { echo "first interrupt orphan check failed" >&2; exit "$pgrep_rc"; }
fi
printf 'FIRST_INTERRUPT_EXIT=%s\n' "$interrupt_rc"
printf 'FIRST_INTERRUPT_STDOUT='; tr '\n' ' ' <"$sandbox/interrupt.stdout"; printf '\n'
printf '%s\n' 'FIRST_INTERRUPT_PROCESS_GROUP=true' 'FIRST_INTERRUPT_ABORTED_ENTRY=true' 'FIRST_INTERRUPT_ORPHANS=0' 'FIRST_INTERRUPT_REGISTRY=stopped,pid=0'

FAKEPI_SCENARIO=slow_stream python3 -c 'import os, sys; os.setpgid(0, 0); os.execv(sys.argv[1], sys.argv[1:])' "$gibson" run quick 'Force shutdown' >"$sandbox/force.stdout" 2>"$sandbox/force.stderr" &
force_pid=$!
for _ in $(seq 1 100); do
	pi_pid="$(python3 -c "import json; d=json.load(open('.gibson/state.json')); print(next((s['pid'] for s in d['sessions'].values() if s['status']=='live'), ''))")"
	if test -n "$pi_pid" && test -s "$sandbox/force.stdout"; then break; fi
	kill -0 "$force_pid" 2>/dev/null || { echo "force proof exited before streaming" >&2; exit 1; }
	sleep 0.02
done
test -n "$pi_pid"
kill -STOP "$pi_pid"
python3 -c 'import os, signal, sys; os.killpg(int(sys.argv[1]), signal.SIGINT)' "$force_pid"
sleep 0.1
force_started="$(python3 -c 'import time; print(time.monotonic())')"
python3 -c 'import os, signal, sys; os.killpg(int(sys.argv[1]), signal.SIGINT)' "$force_pid"
if wait "$force_pid"; then force_rc=0; else force_rc=$?; fi
force_finished="$(python3 -c 'import time; print(time.monotonic())')"
python3 -c 'import sys; elapsed = float(sys.argv[1]) - float(sys.argv[2]); assert elapsed < 5, f"second interrupt took {elapsed:.3f}s"' "$force_finished" "$force_started"
test "$force_rc" -eq 130
if kill -0 "$pi_pid" 2>/dev/null; then echo "second interrupt left pi alive" >&2; kill -KILL "$pi_pid"; exit 1; fi
python3 -c "import json; d=json.load(open('.gibson/state.json')); assert all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values())"
printf 'SECOND_INTERRUPT_EXIT=%s\n' "$force_rc"
printf '%s\n' 'SECOND_INTERRUPT_PROMPT=true' 'SECOND_INTERRUPT_ORPHANS=0' 'SECOND_INTERRUPT_REGISTRY=stopped,pid=0'

if FAKEPI_SCENARIO=crash_mid_stream "$gibson" run quick 'Crash now' >"$sandbox/crash.stdout" 2>"$sandbox/crash.stderr"; then
	echo "expected crash scenario to fail" >&2
	exit 1
else
	crash_rc=$?
fi
test "$crash_rc" -eq 1
grep -Fq 'Partial output before crash.' "$sandbox/crash.stdout"
grep -Fq 'deterministic crash after first delta' "$sandbox/crash.stderr"
python3 -c "import json; d=json.load(open('.gibson/state.json')); assert all(s['status']=='stopped' and s['pid']==0 for s in d['sessions'].values())"
if pgrep -f "$PWD/.gibson/sessions" >/dev/null; then
	echo "crash left an orphan" >&2; exit 1
else
	pgrep_rc=$?; test "$pgrep_rc" -eq 1 || { echo "crash orphan check failed" >&2; exit "$pgrep_rc"; }
fi
printf 'CRASH_EXIT=%s\n' "$crash_rc"
printf 'CRASH_STDOUT='; tr '\n' ' ' <"$sandbox/crash.stdout"; printf '\n'
printf '%s\n' 'CRASH_STDERR_TAIL=preserved' 'CRASH_ORPHANS=0' 'CRASH_REGISTRY=stopped,pid=0'

if FAKEPI_SCENARIO=huge_entry "$gibson" run quick 'Exercise hostile records' >"$sandbox/huge.stdout" 2>"$sandbox/huge.stderr"; then huge_rc=0; else huge_rc=$?; fi
test "$huge_rc" -eq 0 || { echo "huge-entry run exited $huge_rc" >&2; exit "$huge_rc"; }
huge_session="$(python3 -c "import glob,json; matches=[p for p in glob.glob('.gibson/sessions/*.jsonl') if any(json.loads(line).get('customType')=='gibson-hostile-record' for line in open(p))]; assert len(matches)==1; print(matches[0])")"
python3 -c "import json,sys; lines=open(sys.argv[1],'rb').read().splitlines(); records=[json.loads(line) for line in lines]; custom=next(r for r in records if r.get('customType')=='gibson-hostile-record'); assert len(max(lines,key=len))>1048576; assert custom['data']['marker']=='huge-entry'; assert len(custom['data']['payload'])==1048577 and set(custom['data']['payload'])=={'x'}; assistant=next(r['message'] for r in records if r.get('message',{}).get('role')=='assistant'); expected='Unicode: left'+chr(0x2028)+'middle'+chr(0x2029)+'right.'; assert assistant['content'][0]['text']==expected" "$huge_session"
python3 -c "import pathlib,sys; expected='Unicode: left'+chr(0x2028)+'middle'+chr(0x2029)+'right.\\n'; assert pathlib.Path(sys.argv[1]).read_text()==expected" "$sandbox/huge.stdout"
grep -Fq '[tool bash] running' "$sandbox/huge.stderr"
grep -Fq '[tool bash] done' "$sandbox/huge.stderr"
grep -Fq '[notify] hostile record notification' "$sandbox/huge.stderr"
grep -Fq '[error] extension hostile-extension.ts: deterministic extension failure' "$sandbox/huge.stderr"
if grep -Eq 'tool_execution_update|partialResult|gibson-hostile-record|"payload"' "$sandbox/huge.stderr"; then echo "huge-entry proof leaked raw protocol data" >&2; exit 1; fi
huge_stdout_bytes="$(wc -c <"$sandbox/huge.stdout" | tr -d ' ')"
huge_stderr_bytes="$(wc -c <"$sandbox/huge.stderr" | tr -d ' ')"
test "$huge_stderr_bytes" -le 4096
python3 -c "import json,os,sys; h=json.loads(open(sys.argv[1]).readline()); d=json.load(open('.gibson/state.json')); s=d['sessions'][h['id']]; assert s['status']=='stopped' and s['pid']==0; assert os.path.isfile('.gibson/logs/'+h['id']+'.stderr.log')" "$huge_session"
if pgrep -f "$PWD/.gibson/sessions" >/dev/null; then echo "huge-entry proof left an orphan" >&2; exit 1; else pgrep_rc=$?; test "$pgrep_rc" -eq 1 || exit "$pgrep_rc"; fi
test -z "$(git status --porcelain)"
printf 'HUGE_EXIT=%s\n' "$huge_rc"
printf 'HUGE_SESSION_BYTES=%s\n' "$(wc -c <"$huge_session" | tr -d ' ')"
printf 'HUGE_STDOUT_BYTES=%s\n' "$huge_stdout_bytes"
printf 'HUGE_STDERR_BYTES=%s\n' "$huge_stderr_bytes"
printf '%s\n' 'HUGE_RECORD_PRESERVED=true' 'HUGE_UNICODE_SEPARATORS_PRESERVED=true' 'HUGE_TERMINAL_OUTPUT_BOUNDED=true' 'HUGE_STDOUT_ASSISTANT_ONLY=true' 'HUGE_STDERR_DIAGNOSTICS=true' 'HUGE_REGISTRY=stopped,pid=0' 'HUGE_GIT_STATUS=clean' 'HUGE_ORPHANS=0'

FAKEPI_SCENARIO=dialog_confirm python3 -c 'import os, sys; os.setpgid(0, 0); os.execv(sys.argv[1], sys.argv[1:])' "$gibson" run quick '/dialog' >"$sandbox/dialog.stdout" 2>"$sandbox/dialog.stderr" &
dialog_pid=$!
for _ in $(seq 1 500); do
	if grep -Fq 'gibson run cannot answer dialogs' "$sandbox/dialog.stderr"; then break; fi
	kill -0 "$dialog_pid" 2>/dev/null || { echo "dialog proof exited before warning" >&2; exit 1; }
	sleep 0.02
done
if ! grep -Fq '[warning] pi is waiting for a confirm dialog; gibson run cannot answer dialogs; press Ctrl+C to stop' "$sandbox/dialog.stderr"; then echo "expected dialog warning not observed after polling" >&2; cat "$sandbox/dialog.stderr" >&2; exit 1; fi
python3 -c 'import os, signal, sys; os.killpg(int(sys.argv[1]), signal.SIGINT)' "$dialog_pid"
if wait "$dialog_pid"; then dialog_rc=0; else dialog_rc=$?; fi
test "$dialog_rc" -eq 130
test ! -s "$sandbox/dialog.stdout"
dialog_session="$(python3 -c "import glob,json; matches=[p for p in glob.glob('.gibson/sessions/*.jsonl') if any(r.get('message',{}).get('role')=='user' and r['message'].get('content')=='/dialog' for r in map(json.loads,open(p)))]; assert len(matches)==1; print(matches[0])")"
python3 -c "import json,os,sys; records=[json.loads(line) for line in open(sys.argv[1])]; assert any(r.get('message',{}).get('role')=='assistant' and r['message'].get('stopReason')=='aborted' for r in records); h=records[0]; d=json.load(open('.gibson/state.json')); s=d['sessions'][h['id']]; assert s['status']=='stopped' and s['pid']==0; assert os.path.isfile('.gibson/logs/'+h['id']+'.stderr.log')" "$dialog_session"
if pgrep -f "$PWD/.gibson/sessions" >/dev/null; then echo "dialog proof left an orphan" >&2; exit 1; else pgrep_rc=$?; test "$pgrep_rc" -eq 1 || exit "$pgrep_rc"; fi
test -z "$(git status --porcelain)"
printf 'DIALOG_EXIT=%s\n' "$dialog_rc"
printf '%s\n' 'DIALOG_WARNING=true' 'DIALOG_STDOUT_ASSISTANT_ONLY=true' 'DIALOG_ABORTED_ENTRY=true' 'DIALOG_REGISTRY=stopped,pid=0' 'DIALOG_GIT_STATUS=clean' 'DIALOG_ORPHANS=0'
printf '%s\n' 'GIBSON_CLI_PROOF=PASS'
