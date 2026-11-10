import json
from pathlib import Path
import tempfile
import unittest

from collect_review import collect


class ReviewEvidenceTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        files = {
            "review.md": "Найдены дефекты, но это ещё не доказательство полноты.",
            ".devkit/devkit/RULES.architecture.md": "- **ARCH-001.** Проверить решение.",
            ".devkit/devkit/RULES.style.md": "- **STYLE-001.** Имя по смыслу.",
            ".devkit/devkit/RULES.md": "## Тесты обязательны\n- **TEST-001.** Проверить результат.\n## Постороннее\nНЕ ВХОДИТ В ИСТОЧНИК",
            "docs/ARCHITECTURE.md": "Проектный инвариант.",
            "docs/STYLEGUIDE.md": "Проектное имя.",
        }
        for name, text in files.items():
            self.write(name, text)

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def test_missing_audit_is_visible_despite_findings(self):
        result = collect(self.root)
        self.assertIn("Артефакт аудита отсутствует", result)
        for fact in ("ARCH-001", "STYLE-001", "TEST-001", "Проектный инвариант", "Проектное имя"):
            self.assertIn(fact, result)
        self.assertNotIn("НЕ ВХОДИТ В ИСТОЧНИК", result)

    def test_pending_and_violated_evidence_reaches_judge_unchanged(self):
        audit = {"rules": [{"id": "ARCH-001", "applicability": "applicable", "result": "pending"},
                           {"id": "STYLE-001", "result": "violated", "reason": "Название лжёт"}]}
        text = json.dumps(audit, ensure_ascii=False)
        self.write(".devkit/review-audits/one.json", text)
        self.write(".devkit/review-audits/one.json.previous-old", "СТАРЫЙ ВЫВОД")
        result = collect(self.root)
        self.assertIn(text, result)
        self.assertNotIn("СТАРЫЙ ВЫВОД", result)
        self.assertNotIn("Артефакт аудита отсутствует", result)

    def test_invalid_json_cannot_disappear_from_judge_input(self):
        self.write("docs/tasks/CASE-1.review-audit.json", "{broken")
        result = collect(self.root)
        self.assertIn("Неразбираемый JSON", result)
        self.assertIn("{broken", result)


if __name__ == "__main__":
    unittest.main()
