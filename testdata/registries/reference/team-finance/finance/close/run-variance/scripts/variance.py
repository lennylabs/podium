"""variance.py — compute line-item variance vs. forecast."""

import json
import sys


def variance(actuals: dict, forecast: dict) -> list[dict]:
    out = []
    for line, actual in actuals.items():
        expected = forecast.get(line, 0)
        delta = actual - expected
        out.append({"line": line, "actual": actual, "forecast": expected, "delta": delta})
    out.sort(key=lambda r: abs(r["delta"]), reverse=True)
    return out


if __name__ == "__main__":
    payload = json.loads(sys.stdin.read())
    print(json.dumps(variance(payload["actuals"], payload["forecast"]), indent=2))
