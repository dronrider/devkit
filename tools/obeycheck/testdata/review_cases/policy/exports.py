import csv
import io
from xml.sax.saxutils import escape


def export_v2(rows):
    output = io.StringIO()
    writer = csv.writer(output)
    writer.writerows(rows)
    return output.getvalue()


def legacy_export(rows, csv_mode=False):
    """Deprecated: compatibility endpoint scheduled for removal."""
    if csv_mode:
        output = io.StringIO()
        writer = csv.writer(output)
        writer.writerows(rows)
        return output.getvalue()
    return "<rows>" + "".join(
        "<row>" + escape(str(row[0])) + "</row>" for row in rows
    ) + "</rows>"


def new_report(rows):
    return legacy_export(rows, csv_mode=True)
