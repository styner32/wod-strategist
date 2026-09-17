#!/usr/bin/env python3
"""Compare manually annotated media-clock cycles with an activity API summary.

Truth JSON: {"media_duration_secs": 20, "events": [
 {"movement": "Air Squat", "start_secs": 8, "end_secs": 12}]}
Only annotate directly visible complete cycles. Names must match the catalog.
Usage: python3 scripts/evaluate-activity-counts.py truth.json summary.json
"""
import argparse
import json
import math
from collections import defaultdict


def interval(event):
    start, end = event["start_secs"], event["end_secs"]
    if not all(isinstance(x, (int, float)) and math.isfinite(x) for x in (start, end)) or start < 0 or end <= start:
        raise ValueError("Invalid media interval")
    return start, end


def union_seconds(intervals):
    total, previous_end = 0.0, 0.0
    for start, end in sorted(intervals):
        total += max(0, end - max(start, previous_end))
        previous_end = max(previous_end, end)
    return total


def evaluate(truth, summary, tolerance=0.5):
    duration = truth["media_duration_secs"]
    if not isinstance(duration, (int, float)) or not math.isfinite(duration) or duration <= 0:
        raise ValueError("A verified positive media duration is required")
    expected, observed = defaultdict(list), defaultdict(list)
    for event in truth["events"]:
        if interval(event)[1] > duration:
            raise ValueError("Truth event exceeds media duration")
        expected[event["movement"]].append(event)
    for review in summary.get("reviews", []):
        if review["state"] != "completed":
            continue
        for event in review["observations"]["events"]:
            if event["unit"] == "reps" and event["complete"]:
                if interval(event)[1] > duration:
                    raise ValueError("Observed event exceeds media duration")
                observed[event["movement"]].append(event)
    report = {}
    for name in sorted(expected.keys() | observed.keys()):
        actual, predicted = expected[name], observed[name]
        # Maximum one-to-one matching avoids double credit near a chunk boundary.
        matches = {}

        def match(i, seen):
            a, b = interval(actual[i])
            for j, candidate in enumerate(predicted):
                c, d = interval(candidate)
                if j in seen or abs(b - d) > tolerance or max(a, c) >= min(b, d):
                    continue
                seen.add(j)
                if j not in matches or match(matches[j], seen):
                    matches[j] = i
                    return True
            return False

        for i in range(len(actual)):
            match(i, set())
        matched = len(matches)
        report[name] = {
            "truth": len(actual), "reviewed_observations": len(predicted), "matched": matched,
            "missed": len(actual) - matched, "extra": len(predicted) - matched,
            "miss_rate": (len(actual) - matched) / len(actual) if actual else None,
            "excess_rate": (len(predicted) - matched) / len(actual) if actual else None,
        }
    media_gaps, unmapped = [], 0
    for gap in summary.get("unassessed", []):
        if gap["clock"] == "media" and gap.get("start_secs") is not None and gap.get("end_secs") is not None:
            start, end = interval(gap)
            if end > duration:
                raise ValueError("Unassessed interval exceeds media duration")
            media_gaps.append((start, end))
        else:
            unmapped += 1
    seconds = union_seconds(media_gaps)
    return {
        "source_version": summary.get("source_version"), "media_generation": summary.get("media_generation"),
        "review_state": summary.get("review_state"), "end_tolerance_secs": tolerance,
        "movements": report, "unassessed_media_seconds": seconds,
        "unmapped_gap_count": unmapped,
        "unassessed_media_fraction": seconds / duration if unmapped == 0 else None,
        "scope": "reviewed cycles only; not a validation of unseen workout activity",
    }


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("truth")
    parser.add_argument("summary")
    args = parser.parse_args()
    with open(args.truth, encoding="utf-8") as f:
        truth_data = json.load(f)
    with open(args.summary, encoding="utf-8") as f:
        summary_data = json.load(f)
    print(json.dumps(evaluate(truth_data, summary_data), ensure_ascii=False, indent=2))
