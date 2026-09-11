"""stock-forecaster — Python.

Third step of the order-fulfilment workflow: given what the order wants and
what the depot holds, decide what can be picked today, what should be
substituted, and what needs reordering.

The forecast is a Holt linear trend over the last fortnight of daily sales,
which is the sort of thing that ends up in Python in a real shop -- and the
reason this step is not in the same language as the two before it.

Standard library only: no numpy, nothing to install, so it runs wherever a
python3 does.
"""

import json
import logging
import os

logger = logging.getLogger()
logger.setLevel(logging.INFO)

# Holt's linear trend. Level and slope smoothing chosen to react within about
# three days, which is the reorder lead time Harbour's depots work to.
ALPHA = 0.5
BETA = 0.3

SUBSTITUTES = {
    "OAT-1L": "OAT-BAR-1L",
    "SRD-250": "SRD-ORG-250",
    "TOM-VINE-400": "TOM-CHOP-400",
}


def forecast(history):
    """Next-day demand from a daily sales history, rounded up to whole units."""
    if not history:
        return 0
    level, trend = float(history[0]), 0.0
    for value in history[1:]:
        previous = level
        level = ALPHA * value + (1 - ALPHA) * (level + trend)
        trend = BETA * (level - previous) + (1 - BETA) * trend
    return max(0, int(level + trend + 0.999))


def handler(event, context):
    order = event if "orderId" in event else event.get("order", {})
    on_hand = event.get("depotStock", {})
    picks, substitutions, shorts, reorders = [], [], [], []

    for line in order.get("lines", []):
        sku, wanted = line["sku"], line["quantity"]
        available = on_hand.get(sku, {}).get("units", 0)
        history = on_hand.get(sku, {}).get("dailySales", [])

        if available >= wanted:
            picks.append({"sku": sku, "units": wanted})
        else:
            alt = SUBSTITUTES.get(sku)
            if alt and on_hand.get(alt, {}).get("units", 0) >= wanted:
                substitutions.append({"sku": sku, "substitute": alt, "units": wanted})
            else:
                picks.append({"sku": sku, "units": available})
                shorts.append({"sku": sku, "wanted": wanted, "available": available})

        expected = forecast(history)
        # Reorder when what is left after this order will not cover tomorrow.
        if available - wanted < expected:
            reorders.append(
                {
                    "sku": sku,
                    "forecastNextDay": expected,
                    "onHandAfterOrder": max(0, available - wanted),
                    "suggestOrder": expected * 3,  # three days of cover
                }
            )

    logger.info(
        "picked %d lines, %d substitutions, %d short, %d reorders",
        len(picks),
        len(substitutions),
        len(shorts),
        len(reorders),
    )

    order["fulfilment"] = {
        "picks": picks,
        "substitutions": substitutions,
        "shortLines": shorts,
        "reorders": reorders,
        "complete": not shorts,
        "forecastBy": "stock-forecaster (python %s, holt linear trend)"
        % os.environ.get("AWS_EXECUTION_ENV", "python"),
    }
    return order
