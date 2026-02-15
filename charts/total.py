import matplotlib.pyplot as plt
import matplotlib as mpl
from matplotlib.ticker import ScalarFormatter

# --- Theme colors (matching DE-FERENCE PPT) ---
BG_COLOR = '#0d0d0d'
PLOT_BG = '#151520'
PURPLE = '#B07CD8'
LIGHT_PURPLE = '#D4A5F5'
WHITE = '#FFFFFF'
GRAY = '#AAAAAA'
GRID_COLOR = '#2a2a3a'

# --- Data ---
x1 = [3, 4, 5, 6, 7, 8]

# Graph 1
y1_matrix = [0.6226, 0.5812, 0.5562, 0.5798, 0.6367, 0.680]
y1_baseline = [1.5243, 1.9893, 2.8459, 3.6536, 4.6793, 5.723]

# Graph 2
y2_matrix = [9, 13, 17, 21, 25, 29]

# Graph 3
x2 = [500, 1000, 1500, 2000, 2500, 3000, 3500, 4000]
y3_matrix = [0.5603, 0.6688, 0.6273, 0.6858, 0.7085, 0.8719, 0.9671, 0.7907]
y3_baseline = [2.4521, 2.5198, 2.1869, 2.435, 3.076, 4.2538, 4.2366, 5.6819]


def style_ax(ax):
    """Apply DE-FERENCE dark theme to an axes."""
    ax.set_facecolor(PLOT_BG)
    ax.tick_params(axis='both', which='major', labelsize=16, colors=WHITE)
    ax.xaxis.label.set_color(WHITE)
    ax.yaxis.label.set_color(WHITE)
    for spine in ax.spines.values():
        spine.set_color(GRID_COLOR)
        spine.set_linewidth(0.8)
    ax.grid(True, color=GRID_COLOR, linewidth=0.5, alpha=0.5)


# --- Graph 1: Latency vs Involved Shards ---
fig1 = plt.figure(figsize=(10, 6), facecolor=BG_COLOR)
ax1 = fig1.add_axes([0.13, 0.18, 0.75, 0.72])
ax1.plot(x1, y1_matrix, marker='o', markersize=8, linestyle='-', linewidth=2.5,
         color=PURPLE, label='Matrix', zorder=3)
ax1.plot(x1, y1_baseline, marker='s', markersize=8, linestyle='--', linewidth=2.5,
         color=GRAY, label='Baseline', zorder=3)
ax1.set_xticks(x1)
ax1.set_yticks([0, 1, 2, 3, 4, 5, 6])
style_ax(ax1)
ax1.legend(loc='upper left', frameon=False, fontsize=14,
           labelcolor=WHITE, fancybox=False)
ax1.set_xlabel('Number of involved shards', fontsize=18, labelpad=8)
ax1.set_ylabel('Latency (sec)', fontsize=18, labelpad=8)
plt.savefig('latency_involved.png', dpi=300, facecolor=BG_COLOR,
            bbox_inches='tight', pad_inches=0.3)
plt.close(fig1)

# --- Graph 2: Messages vs Involved Shards ---
fig2 = plt.figure(figsize=(10, 6), facecolor=BG_COLOR)
ax2 = fig2.add_axes([0.13, 0.18, 0.75, 0.72])
ax2.plot(x1, y2_matrix, marker='o', markersize=8, linestyle='-', linewidth=2.5,
         color=PURPLE, label='Matrix', zorder=3)
ax2.fill_between(x1, y2_matrix, alpha=0.15, color=PURPLE)
ax2.set_xticks(x1)
ax2.set_yticks([0, 5, 10, 15, 20, 25, 30])
style_ax(ax2)
ax2.legend(loc='upper left', frameon=False, fontsize=14,
           labelcolor=WHITE, fancybox=False)
ax2.set_xlabel('Number of involved shards', fontsize=18, labelpad=8)
ax2.set_ylabel('Number of messages sent', fontsize=18, labelpad=8)
plt.savefig('numberofmessage.png', dpi=300, facecolor=BG_COLOR,
            bbox_inches='tight', pad_inches=0.3)
plt.close(fig2)

# --- Graph 3: Latency vs Injection Rate ---
fig3 = plt.figure(figsize=(10, 6), facecolor=BG_COLOR)
ax3 = fig3.add_axes([0.13, 0.18, 0.75, 0.72])
ax3.plot(x2, y3_matrix, marker='o', markersize=8, linestyle='-', linewidth=2.5,
         color=PURPLE, label='Matrix', zorder=3)
ax3.plot(x2, y3_baseline, marker='s', markersize=8, linestyle='--', linewidth=2.5,
         color=GRAY, label='Baseline', zorder=3)
ax3.set_xticks(x2)
ax3.set_yticks([0, 1, 2, 3, 4, 5, 6])
ax3.set_ylim(bottom=0)
style_ax(ax3)
ax3.legend(loc='upper left', frameon=False, fontsize=14,
           labelcolor=WHITE, fancybox=False)
ax3.set_xlabel('Injection rate (txn/s)', fontsize=18, labelpad=8)
ax3.set_ylabel('Latency (sec)', fontsize=18, labelpad=8)
plt.savefig('latency_injection.png', dpi=300, facecolor=BG_COLOR,
            bbox_inches='tight', pad_inches=0.3)
plt.close(fig3)
