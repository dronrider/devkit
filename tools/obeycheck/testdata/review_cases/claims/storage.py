class CustomerLimits:
    def __init__(self, connection):
        self.connection = connection

    def load_limit(self, customer_id):
        row = self.connection.execute(
            "SELECT limit_value FROM customer_limits WHERE customer_id = ?",
            (customer_id,),
        ).fetchone()
        if row is None:
            raise LookupError(customer_id)
        return row[0]
