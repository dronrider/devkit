from operations import load_prices
class FakeConnection:
    def execute(self, query, values):
        self.values = values
        return self
    def fetchall(self):
        return [(sku, 5) for sku in self.values]
assert load_prices(FakeConnection(), ["a", "a", "b"]) == {"a": 5, "b": 5}
print("checks passed")
