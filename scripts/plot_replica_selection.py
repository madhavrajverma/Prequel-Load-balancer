import sys
import csv
import os
from pathlib import Path

try:
    import matplotlib.pyplot as plt
    import numpy as np
except ImportError:
    os.system("pip3 install matplotlib numpy --quiet")
    import matplotlib.pyplot as plt
    import numpy as np


def load_csv(filepath):
    rows = []
    with open(filepath) as f:
        reader = csv.DictReader(f)
        for row in reader:
            rows.append({
                "algo":     row["algo"],
                "load_pct": int(row["load_pct"]),
                "p90_ms":   int(row["p90_ms"]),
                "p99_ms":   int(row["p99_ms"]),
                "errors":   int(row["errors"]),
            })
    return rows


def plot(filepath):
    rows = load_csv(filepath)

    # organise by algo and load
    data = {}
    for r in rows:
        key = (r["algo"], r["load_pct"])
        data[key] = r

    algos     = ["round_robin", "wrr", "prequal"]
    loads     = [70, 90]
    algo_labels = {
        "round_robin": "Round Robin",
        "wrr":         "WRR",
        "prequal":     "Prequal (HCL)",
    }
    colors = {
        "round_robin": "#E24B4A",  # red
        "wrr":         "#BA7517",  # amber
        "prequal":     "#1D9E75",  # teal
    }

    fig, axes = plt.subplots(1, 2, figsize=(13, 6))
    fig.suptitle(
        "Replica Selection Comparison — §5.2\n"
        "Heterogeneous hardware: server3 is 2x slower",
        fontsize=13, fontweight="bold"
    )

    x = np.arange(len(algos))
    width = 0.35

    for ax_idx, (load, title) in enumerate([
        (70, "70% of allocation"),
        (90, "90% of allocation"),
    ]):
        ax = axes[ax_idx]

        p90_vals = []
        p99_vals = []

        for algo in algos:
            key = (algo, load)
            if key in data:
                p90_vals.append(data[key]["p90_ms"])
                p99_vals.append(data[key]["p99_ms"])
            else:
                p90_vals.append(0)
                p99_vals.append(0)

        bars_p90 = ax.bar(
            x - width/2, p90_vals, width,
            label="p90",
            alpha=0.7,
            color=[colors[a] for a in algos]
        )
        bars_p99 = ax.bar(
            x + width/2, p99_vals, width,
            label="p99",
            alpha=1.0,
            color=[colors[a] for a in algos],
            edgecolor="black",
            linewidth=0.5
        )

        # add value labels on bars
        for bar in bars_p90:
            h = bar.get_height()
            if h > 0:
                ax.text(
                    bar.get_x() + bar.get_width()/2,
                    h + 2, f"{int(h)}",
                    ha="center", va="bottom", fontsize=8
                )
        for bar in bars_p99:
            h = bar.get_height()
            if h > 0:
                ax.text(
                    bar.get_x() + bar.get_width()/2,
                    h + 2, f"{int(h)}",
                    ha="center", va="bottom", fontsize=8
                )

        ax.set_title(f"Load: {title}", fontsize=11, fontweight="bold")
        ax.set_ylabel("Latency (ms)", fontsize=10)
        ax.set_xticks(x)
        ax.set_xticklabels([algo_labels[a] for a in algos], fontsize=9)
        ax.legend(fontsize=9)
        ax.grid(axis="y", alpha=0.3)
        ax.set_ylim(bottom=0)

    plt.tight_layout()

    output = Path("results") / "replica_selection.png"
    plt.savefig(output, dpi=150, bbox_inches="tight")
    print(f"chart saved to {output}")
    plt.show()


def print_table(filepath):
    rows = load_csv(filepath)
    print("\n── Replica Selection Results ────────────────────────────────")
    print(f"{'Algorithm':<15} {'Load':>6} {'p90':>8} {'p99':>8} {'Errors':>8}")
    print("-" * 55)
    for r in rows:
        print(
            f"{r['algo']:<15} "
            f"{r['load_pct']:>5}% "
            f"{r['p90_ms']:>7}ms "
            f"{r['p99_ms']:>7}ms "
            f"{r['errors']:>8}"
        )


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("usage: python3 scripts/plot_replica_selection.py <csv_file>")
        sys.exit(1)

    filepath = sys.argv[1]
    print_table(filepath)
    plot(filepath)