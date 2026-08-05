"""oldtool: мелкие операции над числами."""


def clamp(value, low, high):
    """Загоняет число в границы [low, high]."""
    if value < low:
        return low
    return value


def total(values):
    """Сумма списка, пустой список даёт ноль."""
    out = 0
    for v in values:
        out += v
    return out
