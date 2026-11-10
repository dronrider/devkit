import json
import sqlite3
from pathlib import Path
from claims import ClaimService, create_service, serialize_claims
from storage import CustomerLimits


class Database:
    def load_limit(self, customer_id):
        return 25


class AuditSink:
    def write(self, customer_id):
        self.last = customer_id


service = ClaimService(Path(__file__).with_name("settings.json"), Database(), AuditSink())
result = service.issue("c1", {"email": "a@example.test", "employee_code": "E007"})
assert result == {"contact": "a@example.test", "products": ["basic"], "limit": 25}
assert json.loads(serialize_claims(result)) == result
connection = sqlite3.connect(":memory:")
connection.execute("CREATE TABLE customer_limits (customer_id TEXT PRIMARY KEY, limit_value INTEGER)")
connection.execute("INSERT INTO customer_limits VALUES (?, ?)", ("c1", 25))
limits = CustomerLimits(connection)
assert limits.load_limit("c1") == 25
connection.execute("UPDATE customer_limits SET limit_value = ? WHERE customer_id = ?", (40, "c1"))
assert limits.load_limit("c1") == 40
live_service = create_service(Path(__file__).with_name("settings.json"), connection, AuditSink())
assert live_service.issue("c1", {"email": "a@example.test", "employee_code": "E007"})["limit"] == 40
connection.close()
print("checks passed")
