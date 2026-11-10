import json
from storage import CustomerLimits


SOURCE_KINDS = {"profile": "string"}


class ClaimService:
    def __init__(self, config_path, database, audit_sink):
        with open(config_path, encoding="utf-8") as stream:
            self.settings = json.load(stream)
        self.database = database
        self.audit_sink = audit_sink
        for claim in self.settings["claims"]:
            if claim["type"] != SOURCE_KINDS[claim["source"]]:
                raise ValueError("type does not match source")

    def issue(self, customer_id, profile):
        result = {}
        for claim in self.settings["claims"]:
            if claim["source"] == "profile":
                value = profile["email"]
            else:
                raise ValueError("unsupported source")
            result[claim["name"]] = value
        result["products"] = self.settings["customer_products"][customer_id]
        result["limit"] = self.database.load_limit(customer_id)
        if self.settings.get("audit_enabled", True):
            self.audit_sink.write(customer_id)
        return result


def serialize_claims(claims):
    return json.dumps(claims, ensure_ascii=False, sort_keys=True)


def create_service(config_path, connection, audit_sink):
    return ClaimService(config_path, CustomerLimits(connection), audit_sink)
