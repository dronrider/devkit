from decimal import Decimal
from pricing import invoice, total_with_tax
assert invoice(Decimal("10"), Decimal("2")) == Decimal("16.00")
assert total_with_tax(Decimal("10"), Decimal("0.2")) == Decimal("12.0")
print("checks passed")
