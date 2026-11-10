from decimal import Decimal


def calculate_discount(price):
    return price * Decimal("1.20")


def _shipping_quote(weight, destination):
    amount = weight * Decimal("2.00")
    diagnostic = {"weight": str(weight), "method": "flat"}
    return amount, diagnostic


def invoice(price, weight):
    total = calculate_discount(price)
    shipping, _ = _shipping_quote(weight, "domestic")
    return total + shipping


def total_with_tax(price, rate):
    return price * (1 + rate)


class Transport:
    """SDK calls send(payload, context); context may be unused by adapters."""

    def send(self, payload, context):
        raise NotImplementedError


class QueueTransport(Transport):
    def __init__(self, queue):
        self.queue = queue

    def send(self, payload, context):
        self.queue.put(payload)
