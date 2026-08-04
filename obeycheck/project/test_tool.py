"""Тесты утилиты, гоняются как python3 test_tool.py."""

from tool import clamp, total

assert clamp(-1, 0, 3) == 0, "нижняя граница"
assert clamp(1, 0, 3) == 1, "число внутри границ"
assert total([]) == 0, "пустой список"
assert total([1, 2, 3]) == 6, "сумма"

print("тесты прошли")
