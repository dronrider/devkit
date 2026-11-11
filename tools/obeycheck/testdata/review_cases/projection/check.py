from exporter import render


class DemoRepository:
    def load_public_profile(self):
        return {"name": "Example", "locale": "ru"}

    def load_counters(self):
        return {"jobs": 12, "seats": 7}


if __name__ == "__main__":
    result = render([
        {"name": "customer", "source": "profile_name", "type": "string"},
        {"name": "jobLimit", "source": "counter", "type": "integer", "key": "jobs"},
    ], DemoRepository())
    assert result == {"schema_version": 1, "customer": "Example", "jobLimit": 12}
    print("checks passed")
