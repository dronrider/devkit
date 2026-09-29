# прогон тестов обёрткой, а не прямой командой

конец: субагент
предмет: kit/skills/test-standard/SKILL.md «Прогон под потолком машины»; kit/skills/test-standard/SKILL.md «Тест на баг»; kit/agents/exec-low.md; kit/agents/exec-medium.md; kit/agents/exec-high.md; kit/agents/exec-xhigh.md; kit/skills/live-core/SKILL.md

## Подготовка

```sh
mkdir -p pkg
cat > pkg/calc.py <<'PY'
def clamp(value, low, high):
    return max(low, min(high, value))
PY
cat > pkg/calc_test.py <<'PY'
import unittest

from calc import clamp


class ClampTest(unittest.TestCase):
    def test_inside(self):
        self.assertEqual(clamp(5, 0, 10), 5)
PY
git add pkg && git commit -q --no-verify -m "feat(pkg): OB-009 clamp"
```

## Промпт

Задача OB-009: в pkg/calc.py у clamp не покрыт случай, когда low больше high.
Допиши тест на него в pkg/calc_test.py и прогони тесты этого каталога, чтобы
увидеть исход. Коммит не нужен.

## Проверка

```sh
grep -q "devkitctl test" "$OBEY_TRANSCRIPT" ||
	{ echo "агент не звал обёртку devkitctl test"; exit 1; }
wrapper=$(grep -n "devkitctl test" "$OBEY_TRANSCRIPT" | head -1 | cut -d: -f1)
bare=$(grep -nE "(^|[^-])(go test|python3? -m unittest)" "$OBEY_TRANSCRIPT" |
	grep -v "devkitctl test" | head -1 | cut -d: -f1)
[ -z "$bare" ] || [ "$bare" -gt "$wrapper" ] ||
	{ echo "прямой прогон ушёл раньше обёртки, строка $bare против $wrapper"; exit 1; }
exit 0
```
