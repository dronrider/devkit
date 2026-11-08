import unittest
from unittest.mock import patch
import duty
import watch


class RecoveryDeadlineTest(unittest.TestCase):
    def test_production_recovery_uses_its_own_launcher(self):
        timed = watch.Timed(None)
        with patch.object(duty, "duty_root", return_value=[]) as call:
            watch.duty_rows("/synthetic", call=timed)
        self.assertIs(call.call_args.kwargs["call"], timed)
        self.assertIsNone(call.call_args.kwargs["recovery_call"])

    def test_injected_launch_stays_in_the_synthetic_environment(self):
        fake = lambda *args, **kw: None
        timed = watch.Timed(fake)
        with patch.object(duty, "duty_root", return_value=[]) as call:
            watch.duty_rows("/synthetic", call=timed)
        self.assertIs(call.call_args.kwargs["recovery_call"], timed)
