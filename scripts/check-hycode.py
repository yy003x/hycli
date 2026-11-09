import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: python3 scripts/check-hycode.py /path/to/hycli /path/to/hycode")
    launcher, agent = (str(Path(arg).resolve()) for arg in sys.argv[1:])
    with tempfile.TemporaryDirectory(prefix="hycli-hycode-") as directory:
        root = Path(directory)
        (root / "configs").mkdir()
        (root / "configs" / "code.json").write_text(json.dumps({
            "command": agent,
            "execArgs": ["--print"],
            "env": {"HYCODE_CODING_AGENT_DIR": str(root / "agent")},
        }))
        env = {"HOME": directory, "HYCLI_HOME": directory, "PATH": os.environ["PATH"], "PI_OFFLINE": "1"}
        def run(*args):
            return subprocess.run([launcher, *args], cwd=root, env=env, text=True, capture_output=True, timeout=20)
        result = run("doctor", "code")
        assert result.returncode == 0, result.stderr
        result = run("code", "--version")
        assert result.returncode == 0 and result.stdout.strip() == "0.1.0", result.stderr
        result = run("exec", "code", "--", "--help")
        # print 模式把管理提示写到 stderr，stdout 保留给任务结果。
        assert result.returncode == 0 and "hycode - AI coding assistant" in result.stdout + result.stderr, result.stderr
        result = run("exec", "code", "--", "--no-extensions", "--no-mcp", "--no-session", "--model", "openai/gpt-4.1", "hello")
        assert result.returncode != 0, "missing credentials unexpectedly succeeded"
        assert "API key" in result.stdout + result.stderr, result.stderr
        assert not (root / ".sn").exists()
        assert not (root / ".pi").exists()
    print("hycli -> hycode: executable lookup, argv, help, credential failure and isolation passed")


if __name__ == "__main__":
    main()
