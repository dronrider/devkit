import unittest
from unittest import mock

import devkitctl_test as doctor_tests
from testenv import SandboxCase


class DeployFindingContractTest(SandboxCase):
    def run_original_check(self, output, suffix):
        case = doctor_tests.DeployTest("test_06_config_is_not_gitignored")
        case.box = self.box
        project = self.box.project
        with mock.patch.object(self.box, "project",
                               side_effect=lambda name: project(name + "-" + suffix)):
            with mock.patch.object(case, "sysdoctor", return_value=(1, output)) as doctor:
                try:
                    case.test_06_config_is_not_gitignored()
                finally:
                    doctor.assert_called_once()

    def test_rejects_neighbor_gitignore_finding(self):
        output = ".devkit/cmdout не гитигнорнут\n"
        with self.assertRaisesRegex(AssertionError,
                                    "нет находки про негитигнорнутый конфиг"):
            self.run_original_check(output, "neighbor")

    def test_accepts_deploy_config_finding(self):
        self.run_original_check(".devkit/deploy.local не гитигнорнут\n", "deploy")


if __name__ == "__main__":
    unittest.main()
