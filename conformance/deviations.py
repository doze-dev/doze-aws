"""Where doze-aws differs from AWS on purpose.

A difference the suite finds is a bug until somebody decides otherwise. This
file is where that decision is written down: the scenario and label, and the
reason. A listed difference does not fail the run; it is counted and printed
in the summary every time, so it stays a decision rather than becoming a habit.

It ratchets in both directions. A difference that is not listed fails. A
listing that no longer differs fails too — fix the behaviour and the entry has
to go with it.

Key: "<module>::<test>::<label>".
"""

# Empty, and meant to stay that way: doze-aws is not more forgiving than AWS.
# What belongs here is only what cannot exist on a laptop at all.
DEVIATIONS = {}
