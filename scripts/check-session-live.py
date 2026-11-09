import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import subprocess
import tempfile
import threading
import time


def main():
    parser = argparse.ArgumentParser(description="Verify real Pi/Codex multi-turn sessions using existing agent credentials")
    parser.add_argument("hycli")
    parser.add_argument("--live", action="store_true", required=True, help="explicitly allow model requests")
    parser.add_argument("--agent", choices=["pi", "codex"], action="append")
    parser.add_argument("--codex-approval-only", action="store_true", help="test a real Codex approval and temporary-file write instead of lifecycle cases")
    args = parser.parse_args()
    launcher = str(Path(args.hycli).resolve())
    root = Path(tempfile.mkdtemp(prefix="hycli-session-live-"))
    (root / "configs").mkdir(mode=0o700)
    work = root / "work"
    work.mkdir()
    env = dict(os.environ, HYCLI_HOME=str(root), PI_OFFLINE="1", PI_TELEMETRY="0")
    receipt = {"root": str(root), "agents": []}

    def run(*command, check=True, timeout=180):
        result = subprocess.run([launcher, "session", *command], cwd=work, env=env, text=True, capture_output=True, timeout=timeout)
        if check and result.returncode:
            raise RuntimeError(f"{command[0]} failed: {result.stderr[-2000:]}")
        return result

    def state(operation, identifier):
        result = run(operation, identifier, check=False)
        if result.stdout:
            current = json.loads(result.stdout)["session"]
            # Agent 退出后，worker 关闭监听与最终状态落盘之间允许短暂失联。
            if not result.returncode or (operation == "status" and current["state"] == "unreachable"):
                return current
        raise RuntimeError(f"{operation} failed: {result.stderr[-2000:]}")

    def wait_state(identifier, predicate):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            current = state("status", identifier)
            if predicate(current):
                return current
            time.sleep(0.05)
        raise RuntimeError(f"session did not reach expected state: {current['state']}")

    def send(identifier, prompt):
        result = run("send", "--wait", identifier, prompt)
        text = []
        terminal = None
        for line in result.stdout.splitlines():
            record = json.loads(line)
            event = record.get("event", {})
            if event.get("type") == "turn.completed":
                terminal = event["data"]
            raw = event.get("data", {})
            if event.get("type") != "agent.event" or not isinstance(raw, dict):
                continue
            if raw.get("type") == "message_end" and raw.get("message", {}).get("role") == "assistant":
                text = [item["text"] for item in raw["message"].get("content", []) if item.get("type") == "text"]
            if raw.get("method") == "item/completed":
                item = raw.get("params", {}).get("item", {})
                if item.get("type") == "agentMessage":
                    text = [item.get("text", "")]
        assert terminal and terminal["state"] == "completed", terminal
        return "".join(text)

    try:
        if args.codex_approval_only:
            executable = shutil.which("codex")
            if not executable:
                raise RuntimeError("codex is not installed")
            native_args = ["-c", 'model_reasoning_effort="low"', "-c", 'approval_policy="on-request"', "-c", 'sandbox_mode="read-only"']
            (root / "configs/codex.json").write_text(json.dumps({"command": executable, "session": {"adapter": "codex", "args": native_args}}))
            opened = state("open", "codex")
            identifier = opened["id"]
            marker = "hycli-approval-check.txt"
            prompt = f"Use the shell tool to run exactly this command in the current directory: printf 'APPROVAL_TEST\\n' > {marker}. This session is read-only: request escalated permission for this one write and wait for the human approval response. Do not write any other file. Then reply DONE."
            approvals = []
            with tempfile.TemporaryFile(mode="w+") as errors:
                process = subprocess.Popen([launcher, "session", "send", "--wait", identifier, prompt], cwd=work, env=env, text=True, stdout=subprocess.PIPE, stderr=errors)
                timer = threading.Timer(180, process.kill)
                timer.start()
                try:
                    for line in process.stdout:
                        event = json.loads(line).get("event", {})
                        if event.get("type") != "input.required":
                            continue
                        request = event["data"]["request"]
                        params = event["data"]["params"]
                        if request["method"] != "item/commandExecution/requestApproval" or marker not in params.get("command", ""):
                            raise RuntimeError(f"unexpected approval request: {request['method']}")
                        run("respond", identifier, request["id"], '{"decision":"accept"}')
                        approvals.append(request["method"])
                    code = process.wait(timeout=10)
                    errors.seek(0)
                    assert code == 0, errors.read()[-2000:]
                finally:
                    timer.cancel()
                    if process.poll() is None:
                        process.kill()
                    process.wait()
            assert approvals, "no real approval request was emitted"
            assert (work / marker).read_text() == "APPROVAL_TEST\n"
            receipt["agents"].append({"adapter": "codex", "id": identifier, "approvalRoundTrip": approvals, "temporaryFileVerified": True})
            print("codex: approval round trip and temporary-file readback passed", flush=True)
        for agent in ([] if args.codex_approval_only else (args.agent or ["pi", "codex"])):
            executable = shutil.which(agent)
            if not executable:
                raise RuntimeError(f"{agent} is not installed")
            native_args = []
            if agent == "pi":
                native_args = ["--offline", "--no-extensions", "--no-mcp", "--no-skills", "--no-context-files", "--no-tools", "--thinking", "low", "--session-dir", str(root / "pi-history")]
            else:
                native_args = ["-c", 'model_reasoning_effort="low"']
            (root / "configs" / f"{agent}.json").write_text(json.dumps({"command": executable, "session": {"adapter": agent, "args": native_args}}))
            opened = state("open", agent)
            identifier = opened["id"]
            print(f"{agent}: ready {identifier}", flush=True)
            token = secrets.token_hex(8)
            send(identifier, f"Remember this session token: {token}. Reply only STORED. Do not call tools.")
            answer = send(identifier, "What is the session token I asked you to remember? Reply with only the token. Do not call tools.")
            assert token in answer, f"{agent}: context was not retained"
            current = state("status", identifier)
            assert current["agentPid"] == opened["agentPid"] and current["native"]["id"] == opened["native"]["id"]
            closed = state("close", identifier)
            assert closed["state"] == "closed"
            resumed = state("resume", identifier)
            assert resumed["instance"] != opened["instance"] and resumed["native"]["id"] == opened["native"]["id"]
            answer = send(identifier, "What is the session token I asked you to remember? Reply with only the token. Do not call tools.")
            assert token in answer, f"{agent}: native resume lost context"
            active = json.loads(run("send", identifier, "Print the integers from 1 to 50000, one per line. Do not call tools.").stdout)["session"]
            busy = run("send", identifier, "This must not be queued", check=False)
            assert busy.returncode and "concurrent sends are not queued" in busy.stderr, busy.stderr
            state("interrupt", identifier)
            interrupted = wait_state(identifier, lambda value: value.get("turn", {}).get("state") not in ["submitted", "running", "interrupting"])
            assert interrupted["turn"]["state"] == "interrupted", interrupted["turn"]
            # 只终止刚由此测试创建并确认身份的 Agent，验证 worker 对真实进程退出的处理。
            assert interrupted["instance"] == resumed["instance"]
            os.kill(interrupted["agentPid"], signal.SIGTERM)
            failed = wait_state(identifier, lambda value: value["state"] == "failed")
            receipt["agents"].append({"adapter": agent, "id": identifier, "native": opened["native"], "firstInstance": opened["instance"], "resumedInstance": resumed["instance"], "multiTurn": True, "resume": True, "busyRejected": True, "interrupted": True, "agentExitDetected": failed["state"]})
            print(f"{agent}: multi-turn, resume, busy rejection, interrupt and agent exit passed", flush=True)
    except Exception as error:
        receipt["error"] = str(error)
        raise
    finally:
        cleanup = run("list", check=False)
        if cleanup.returncode == 0:
            for item in json.loads(cleanup.stdout).get("sessions", []):
                result = run("close", item["id"], check=False)
                if result.returncode:
                    print(f"cleanup failed for {item['id']}: {result.stderr}", flush=True)
        (root / "receipt.json").write_text(json.dumps(receipt, indent=2))
        print(f"receipt: {root / 'receipt.json'}", flush=True)


if __name__ == "__main__":
    main()
