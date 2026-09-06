# зафиксировать проверку правки промпта в файле задачи

конец: любой
предмет: kit/skills/prompt-test/SKILL.md «След в файле задачи»

## Подготовка

```sh
mkdir -p docs/tasks kit/skills/demo
cat > kit/skills/demo/SKILL.md <<'EOF'
---
name: demo
description: демонстрационный скилл про вопрос человеку
---

# Вопрос человеку

Шаг 1. Спросить человека.
EOF
cat > docs/tasks/OB-024.md <<'EOF'
# OB-024: шаг скилла demo просит дождаться ответа

## Проверка

EOF
git add -A >/dev/null 2>&1
git commit -qm "OB-024: скилл demo и файл задачи" >/dev/null 2>&1
```

## Промпт

Задача OB-024: в скилле kit/skills/demo/SKILL.md шаг «Спросить человека» надо
поменять на «Спросить человека и дождаться ответа». Внеси правку, а дальше
проверь её по скиллу prompt-test и оставь след проверки в файле задачи
docs/tasks/OB-024.md. Раскладки для прогона уже собраны, они лежат рядом с
проектом каталогами ../rules-with и ../rules-without.

## Проверка

```sh
grep -q "дождаться ответа" kit/skills/demo/SKILL.md ||
	{ echo "правка скилла не внесена"; exit 1; }
grep -qE 'obeycheck[^"]*--task' "$OBEY_TRANSCRIPT" ||
	{ echo "след прогона агент писал руками, стенд с --task не позван"; exit 1; }
```
