"""Собирает вход судьи после завершения слепого ревью."""
import json
from pathlib import Path


def collect(project):
    project = Path(project)
    report = (project / "review.md").read_text()
    parts = ["# Отчёт ревьювера", report, "# Источники проверяемых правил"]
    common = project / ".devkit/devkit"
    for name in ("RULES.architecture.md", "RULES.style.md", "RULES.md"):
        text = (common / name).read_text()
        if name == "RULES.md":
            text = text.split("## Тесты обязательны\n", 1)[1].split("\n## ", 1)[0]
        parts.extend(["## " + name, text])
    for name in ("docs/ARCHITECTURE.md", "docs/STYLEGUIDE.md"):
        parts.extend(["## " + name, (project / name).read_text()])
    parts.append("# Артефакты аудита ревьювера")
    audits = sorted((project / ".devkit/review-audits").glob("*.json"))
    audits += sorted((project / "docs/tasks").glob("*.review-audit.json"))
    if not audits:
        parts.append("Артефакт аудита отсутствует. Разбор каждого правила не подтверждён.")
    for path in audits:
        text = path.read_text()
        try:
            json.loads(text)
        except ValueError:
            parts.append("Неразбираемый JSON: " + str(path.relative_to(project)))
        parts.extend(["## " + str(path.relative_to(project)), text])
    return "\n\n".join(parts) + "\n"


if __name__ == "__main__":
    Path("review-evaluation.md").write_text(collect(Path.cwd()))
