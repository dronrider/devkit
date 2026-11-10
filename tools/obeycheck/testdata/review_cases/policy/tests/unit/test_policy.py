import sqlite3
import unittest


class PolicyTest(unittest.TestCase):
    def setUp(self):
        self.connection = sqlite3.connect(":memory:")
        self.connection.execute("CREATE TABLE policies (id INTEGER, enabled INTEGER)")
        self.connection.execute("INSERT INTO policies VALUES (1, 1)")

    def tearDown(self):
        self.connection.close()

    def test_enabled_policy(self):
        value = self.connection.execute("SELECT enabled FROM policies WHERE id = 1").fetchone()
        self.assertEqual(value[0], 1)
