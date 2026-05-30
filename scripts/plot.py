
import sys
import csv
import os
from pathlib import Path

try:
    import matplotlib.pyplot as plt
    import matplotlib.patches as mpatches
except ImportError:
    print("installing matplotlib...")
    os.system("pip3 install matplotlib --quiet")
    import matplotlib.pyplot as plt
    import matplotlib.patches as mpatches


def load_csv(filepath):
    """Load experiment CSV and return rows as list of dicts."""
    rows = []
    with open(filepath) as f:
        reader = csv.DictReader(f)
        for row in reader:
            rows.append({
                "step":      int(row["step"]),
                "load_pct":  int(row["load_pct"]),
                "rps":       int(row["rps"]),
                "algo":      row["algo"],
                "p50_ms":    int(row["p50_ms"]),
                "p99_ms":    int(row["p99_ms"]),
                "p999_ms":   int(row["p999_ms"]),
                "errors":    int(row["errors"]),
                "requests":  int(row["requests"]),
            })
    return rows


def plot_comparison(files):
    """Plot p99 and p99.9 latency for each algorithm."""
    datasets = {}
    for f in files:
        rows = load_csv(f)
        if not rows:
            continue
        algo = rows[0]["algo"]
        datasets[algo] = rows

    if not datasets:
        print("no data to plot")
        return

    fig, axes = plt.subplots(1, 2, figsize=(14, 6))
    fig.suptitle(
        "Prequal vs WRR — Load Ramp Experiment (§5.1)\n"
        "Step 4+ simulates above-allocation contention",
        fontsize=13,
        fontweight="bold"
    )

    colors = {
        "prequal": "#1D9E75",  # teal  Prequal
        "wrr":     "#E24B4A",  # red  WRR
    }

    load_labels = ["75%", "83%", "93%", "103%", "114%", "127%", "141%", "157%", "174%"]

    for ax_idx, (metric, title, ylabel) in enumerate([
        ("p99_ms",  "p99 Latency",   "Latency (ms)"),
        ("p999_ms", "p99.9 Latency", "Latency (ms)"),
    ]):
        ax = axes[ax_idx]

        for algo, rows in datasets.items():
            x = [r["load_pct"] for r in rows]
            y = [r[metric] for r in rows]
            color = colors.get(algo, "#888780")

            ax.plot(x, y,
                    label=algo.upper(),
                    color=color,
                    linewidth=2.5,
                    marker="o",
                    markersize=6)

        
        ax.axvline(x=103, color="#BA7517", linestyle="--",
                   linewidth=1.5, alpha=0.8)
        ax.text(104, ax.get_ylim()[1] * 0.95 if ax.get_ylim()[1] > 0 else 100,
                "← above alloc",
                color="#BA7517", fontsize=9)

        ax.set_title(title, fontsize=12, fontweight="bold")
        ax.set_xlabel("Aggregate Load (% of allocation)", fontsize=10)
        ax.set_ylabel(ylabel, fontsize=10)
        ax.set_xticks([75, 83, 93, 103, 114, 127, 141, 157, 174])
        ax.set_xticklabels(load_labels, rotation=45, fontsize=8)
        ax.legend(fontsize=10)
        ax.grid(True, alpha=0.3)
        ax.set_ylim(bottom=0)

    plt.tight_layout()

    output = Path("results") / "comparison.png"
    plt.savefig(output, dpi=150, bbox_inches="tight")
    print(f"chart saved to {output}")
    plt.show()


def print_table(files):
    """Print a summary table to terminal."""
    print("\n Results Summary ")
    print(f"{'Algo':<10} {'Load':>6} {'RPS':>6} {'p50':>6} {'p99':>7} {'p99.9':>8} {'Errors':>8}")
    print("-" * 60)

    for f in files:
        rows = load_csv(f)
        for r in rows:
            print(
                f"{r['algo']:<10} "
                f"{r['load_pct']:>5}% "
                f"{r['rps']:>6} "
                f"{r['p50_ms']:>5}ms "
                f"{r['p99_ms']:>6}ms "
                f"{r['p999_ms']:>7}ms "
                f"{r['errors']:>8}"
            )
        print()


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("usage: python3 scripts/plot.py <csv_files...>")
        print("example: python3 scripts/plot.py results/load_ramp_prequal_*.csv results/load_ramp_wrr_*.csv")
        sys.exit(1)

    files = sys.argv[1:]
    print_table(files)
    plot_comparison(files)