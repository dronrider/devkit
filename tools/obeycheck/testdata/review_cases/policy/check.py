from exports import export_v2, legacy_export, new_report
from settings import connection_url
assert new_report([["a", 2]]) == export_v2([["a", 2]])
assert legacy_export([["<customer>"]]) == "<rows><row>&lt;customer&gt;</row></rows>"
assert connection_url({"database_url": "local"}, {"SERVICE_DATABASE_URL": "remote"}) == "remote"
print("checks passed")
