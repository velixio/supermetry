import base64
import json
import os
import stat
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from ab import stack


class StackHelpersTest(unittest.TestCase):
    def test_render_mkkey_quotes_only_a_valid_random_key(self) -> None:
        key = "ab" * 24

        rendered = stack.render_mkkey(key)

        self.assertIn(f'"{key}"', rendered)
        self.assertIn('"accuracy-ab"', rendered)
        self.assertNotIn(stack.KEY_PLACEHOLDER, rendered)
        with self.assertRaises(ValueError):
            stack.render_mkkey('"; System.cmd("sh", ["-c", "bad"])')

    def test_supermetry_env_is_isolated_from_owner_server(self) -> None:
        secret = base64.b64encode(bytes(range(32))).decode()

        with mock.patch.dict(
            os.environ,
            {
                "SUPERMETRY_DATABASE_HOST": "owner-db.example.test",
                "SUPERMETRY_ADMIN_API_KEY": "owner-key",
            },
        ):
            env = stack.supermetry_env(Path("/tmp/accuracy-ab"), secret)

        self.assertEqual(env["SUPERMETRY_LISTEN"], "127.0.0.1:18090")
        self.assertEqual(env["SUPERMETRY_TRACKING_MODE"], "identified")
        self.assertEqual(env["SUPERMETRY_TRUSTED_PROXIES"], "127.0.0.1/32")
        self.assertEqual(env["SUPERMETRY_BASE_URL"], "http://app.test:18090")
        self.assertIn("/accuracy_ab?", env["SUPERMETRY_DATABASE_URL"])
        self.assertNotIn(":8787", json.dumps(env))
        self.assertNotIn("/supermetry?", env["SUPERMETRY_DATABASE_URL"])
        self.assertNotIn("SUPERMETRY_DATABASE_HOST", env)
        self.assertNotIn("SUPERMETRY_ADMIN_API_KEY", env)

    def test_parse_reset_token_accepts_cli_output_with_surrounding_text(self) -> None:
        output = "created owner\nhttp://app.test:18090/password-reset?token=a%2Fb-c_9\n"

        self.assertEqual(stack.parse_reset_token(output), "a/b-c_9")

    def test_parse_reset_token_rejects_output_without_token(self) -> None:
        with self.assertRaisesRegex(ValueError, "reset token"):
            stack.parse_reset_token("password reset email sent\n")

    def test_wait_until_retries_until_value_is_truthy(self) -> None:
        results = iter([None, False, "ready"])
        sleeps: list[float] = []

        result = stack.wait_until(
            lambda: next(results),
            timeout=5,
            interval=0.25,
            clock=mock.Mock(side_effect=[0.0, 0.1, 0.2]),
            sleep=sleeps.append,
        )

        self.assertEqual(result, "ready")
        self.assertEqual(sleeps, [0.25, 0.25])

    def test_wait_until_times_out_without_an_extra_call(self) -> None:
        probe = mock.Mock(return_value=False)

        with self.assertRaisesRegex(TimeoutError, "timed out"):
            stack.wait_until(
                probe,
                timeout=1,
                interval=0.1,
                clock=mock.Mock(side_effect=[0.0, 0.5, 1.0]),
                sleep=mock.Mock(),
            )

        self.assertEqual(probe.call_count, 2)


class StackLifecycleTest(unittest.TestCase):
    def test_up_cleans_plausible_after_failure_before_supermetry_starts(self) -> None:
        calls: list[list[str]] = []

        def run(args: list[str], **_kwargs):
            calls.append(args)
            if "docker-compose.dev-db.yml" in " ".join(args):
                raise RuntimeError("database startup failed")
            return mock.Mock(stdout="")

        with tempfile.TemporaryDirectory() as directory, mock.patch.object(stack, "_run", run), mock.patch.object(
            stack, "_docker", return_value=["docker"]
        ), mock.patch.object(stack, "_plausible_query", return_value=True):
            with self.assertRaisesRegex(RuntimeError, "database startup failed"):
                stack.up(workdir=Path(directory))

        self.assertTrue(
            any(args[-3:] == ["plausible", "--purge"] or args[-2:] == ["plausible", "--purge"] for args in calls),
            calls,
        )

    def test_drop_database_tolerates_missing_postgres_container(self) -> None:
        missing = stack.subprocess.CalledProcessError(1, ["docker", "exec"])
        with mock.patch.object(stack, "_docker", return_value=["docker"]), mock.patch.object(
            stack, "_run", side_effect=missing
        ), mock.patch.object(stack, "_container_running", return_value=False):
            stack._drop_database("accuracy_ab")

    def test_down_runs_every_cleanup_when_an_earlier_step_fails(self) -> None:
        calls: list[str] = []

        def fail_kill(_pid: int) -> None:
            calls.append("kill")
            raise RuntimeError("cannot kill")

        with tempfile.TemporaryDirectory() as directory:
            state_path = Path(directory) / "stack.json"
            state_path.write_text(
                json.dumps({"supermetry": {"pid": 321, "database": "accuracy_ab"}}),
                encoding="utf-8",
            )
            with self.assertRaisesRegex(RuntimeError, "cannot kill"):
                stack.down(
                    state_path=state_path,
                    kill_process=fail_kill,
                    drop_database=lambda name: calls.append(f"drop:{name}"),
                    down_plausible=lambda: calls.append("plausible"),
                )

            self.assertEqual(calls, ["kill", "drop:accuracy_ab", "plausible"])
            self.assertFalse(state_path.exists())

    def test_write_state_is_private_and_atomic(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "stack.json"

            stack.write_state(path, {"secret": "not-printed"})

            self.assertEqual(json.loads(path.read_text(encoding="utf-8"))["secret"], "not-printed")
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)


if __name__ == "__main__":
    unittest.main()
