#!/usr/bin/env python3
"""
Compare the Python gateway CLI against the embedded C++ wrapper.

The C++ gateway is a thin wrapper around gateway/imessage_client.py that embeds
CPython to skip Python process startup overhead. This script runs a small
suite of commands with both binaries and reports timing statistics.
"""

import argparse
import json
import statistics
import subprocess
import time
from dataclasses import dataclass, asdict
from pathlib import Path
from typing import Callable, List

REPO_ROOT = Path(__file__).parent.parent
PYTHON_CLI = Path(__file__).parent / "imessage_client.py"
DEFAULT_CPP = Path(__file__).parent / "cpp" / "imessage_gateway"


@dataclass
class BenchmarkResult:
    runner: str
    name: str
    description: str
    iterations: int
    mean_ms: float
    median_ms: float
    min_ms: float
    max_ms: float
    std_dev_ms: float
    success_rate: float


CommandRunner = Callable[[List[str]], tuple[float, bool, str]]


def run_once(command: List[str]) -> tuple[float, bool, str]:
    start = time.perf_counter()
    try:
        result = subprocess.run(
            command,
            capture_output=True,
            text=True,
            cwd=str(REPO_ROOT),
        )
        elapsed = (time.perf_counter() - start) * 1000
        success = result.returncode == 0
        output = result.stdout if success else result.stderr
        return elapsed, success, output
    except Exception as exc:  # pragma: no cover - defensive logging
        elapsed = (time.perf_counter() - start) * 1000
        return elapsed, False, str(exc)


def benchmark_command(
    runner_name: str,
    description: str,
    cmd: List[str],
    run_command: CommandRunner,
    iterations: int,
) -> BenchmarkResult:
    timings: List[float] = []
    successes = 0

    for _ in range(iterations):
        elapsed, success, _ = run_command(cmd)
        timings.append(elapsed)
        if success:
            successes += 1

    return BenchmarkResult(
        runner=runner_name,
        name=" ".join(cmd),
        description=description,
        iterations=iterations,
        mean_ms=statistics.mean(timings),
        median_ms=statistics.median(timings),
        min_ms=min(timings),
        max_ms=max(timings),
        std_dev_ms=statistics.stdev(timings) if len(timings) > 1 else 0.0,
        success_rate=(successes / iterations) * 100,
    )


def build_runner(prefix: List[str]) -> CommandRunner:
    def runner(cmd: List[str]) -> tuple[float, bool, str]:
        return run_once(prefix + cmd)

    return runner


def run_suite(
    runner_name: str,
    run_command: CommandRunner,
    iterations: int,
) -> List[BenchmarkResult]:
    suite = [
        ("startup", "--help", ["--help"]),
        ("contacts", "List contacts (JSON)", ["contacts", "--json"]),
        ("recent10", "Recent conversations (10)", ["recent", "--limit", "10"]),
        ("unread", "Unread messages", ["unread"]),
        ("analytics7", "Analytics (7 days)", ["analytics", "--days", "7", "--json"]),
    ]

    results: List[BenchmarkResult] = []
    for _, description, cmd in suite:
        results.append(
            benchmark_command(
                runner_name=runner_name,
                description=description,
                cmd=cmd,
                run_command=run_command,
                iterations=iterations,
            )
        )
    return results


def align_results(
    python_results: List[BenchmarkResult], cpp_results: List[BenchmarkResult]
):
    keyed_cpp = {res.name: res for res in cpp_results}
    comparisons = []
    for py_res in python_results:
        cpp_res = keyed_cpp.get(py_res.name)
        if not cpp_res:
            continue
        speedup = (
            py_res.mean_ms / cpp_res.mean_ms if cpp_res.mean_ms > 0 else float("inf")
        )
        comparisons.append((py_res, cpp_res, speedup))
    return comparisons


def print_summary(python_results: List[BenchmarkResult], cpp_results: List[BenchmarkResult]):
    comparisons = align_results(python_results, cpp_results)

    print("\n=== Python vs C++ Gateway ===\n")
    print(f"{'Command':30s} {'Python (ms)':>15s} {'C++ (ms)':>15s} {'Speedup':>10s}")
    print("-" * 75)
    for py_res, cpp_res, speedup in comparisons:
        print(
            f"{py_res.name:30s} "
            f"{py_res.mean_ms:15.2f} "
            f"{cpp_res.mean_ms:15.2f} "
            f"{speedup:10.2f}x"
        )

    print("\nSuccess rates:")
    for label, results in (("Python", python_results), ("C++", cpp_results)):
        failed = [r for r in results if r.success_rate < 100]
        if failed:
            print(f"- {label}: {len(failed)} failures ({', '.join(r.name for r in failed)})")
        else:
            print(f"- {label}: 100% success")


def parse_args():
    parser = argparse.ArgumentParser(
        description="Benchmark C++ gateway wrapper vs Python CLI"
    )
    parser.add_argument(
        "--cpp-binary",
        type=Path,
        default=DEFAULT_CPP,
        help="Path to compiled C++ gateway binary",
    )
    parser.add_argument(
        "--iterations",
        type=int,
        default=5,
        help="Iterations per command (default: 5)",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Output raw results as JSON",
    )
    return parser.parse_args()


def main():
    args = parse_args()

    if not args.cpp_binary.exists():
        raise SystemExit(
            f"C++ binary not found at {args.cpp_binary}. Build it first (make -C gateway/cpp)."
        )

    python_runner = build_runner(["python3", str(PYTHON_CLI)])
    cpp_runner = build_runner([str(args.cpp_binary)])

    python_results = run_suite("python", python_runner, args.iterations)
    cpp_results = run_suite("cpp", cpp_runner, args.iterations)

    if args.json:
        payload = {
            "python": [asdict(r) for r in python_results],
            "cpp": [asdict(r) for r in cpp_results],
        }
        print(json.dumps(payload, indent=2))
    else:
        print_summary(python_results, cpp_results)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
