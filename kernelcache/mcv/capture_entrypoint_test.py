import importlib.util
import pathlib
import unittest


ENTRYPOINT = pathlib.Path(__file__).with_name("capture-entrypoint.py")
SPEC = importlib.util.spec_from_file_location("capture_entrypoint", ENTRYPOINT)
capture_entrypoint = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(capture_entrypoint)


class CaptureEntrypointTest(unittest.TestCase):
    def test_report_stops_when_capture_target_is_deleted(self):
        original = capture_entrypoint.reporter_request
        try:
            def target_gone(_):
                raise capture_entrypoint.CaptureTargetGone()

            capture_entrypoint.reporter_request = target_gone
            with self.assertRaises(capture_entrypoint.CaptureTargetGone):
                capture_entrypoint.report({"state": "Capturing"})
        finally:
            capture_entrypoint.reporter_request = original


if __name__ == "__main__":
    unittest.main()
