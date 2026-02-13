import matplotlib.pyplot as plt
from matplotlib.ticker import ScalarFormatter

# 데이터
x1 = [3, 4, 5, 6, 7, 8] # #involved shard

# Graph 1 데이터
y1_matrix = [0.6226, 0.5812, 0.5562, 0.5798, 0.6367, 0.680]
y1_baseline = [1.5243, 1.9893, 2.8459, 3.6536, 4.6793, 5.723]
# y1_omni = [5.828, 5.813, 5.838, 5.812]

# Graph 2 데이터
y2_matrix = [9, 13, 17, 21, 25, 29]
# y2_baseline = [235, 217, 174, 139]
# y2_omni = [104, 101, 100, 103]

# 데이터
x2 = [500, 1000, 1500, 2000, 2500, 3000, 3500, 4000]
# Graph 3 데이터
y3_matrix = [0.5603, 0.6688, 0.6273, 0.6858, 0.7085, 0.8719, 0.9671, 0.7907]
y3_baseline = [2.4521, 2.5198, 2.1869, 2.435, 3.076, 4.2538, 4.2366, 5.6819]
# y3_omni = [104, 102, 200, 309]

# 공통 설정 (가운데 정렬을 위해 left와 width 조정, left를 늘려 왼쪽 여백 확보)
left = 0.15  # 왼쪽 여백 증가
bottom = 0.25
width = 0.7   # width 감소하여 가운데 정렬 (left 0.15, right 0.15)
height = 0.65
legend_loc = 'center right'
# move slightly to the upper
legend_bbox_to_anchor = (1.00, 0.475)

# Graph 1 생성 및 저장
fig1 = plt.figure(figsize=(10, 6))
ax1 = fig1.add_axes([left, bottom, width, height])
ax1.plot(x1, y1_matrix, marker='o', linestyle='-', color='blue', label='Matrix')
ax1.plot(x1, y1_baseline, marker='o', linestyle='-', color='red', label='Baseline')
# ax1.plot(x, y1_omni, marker='o', linestyle='-', color='black', label='OmniLedger')
ax1.set_xticks(x1)
ax1.set_yticks([0, 1, 2, 3, 4, 5, 6])
ax1.grid(False)
ax1.legend(loc=legend_loc, bbox_to_anchor=legend_bbox_to_anchor, frameon=False, fontsize=15)
ax1.set_xlabel('Number of involved shard', fontsize=20, labelpad=5)
ax1.set_ylabel('Latency (sec)', fontsize=20, labelpad=5)
ax1.tick_params(axis='both', which='major', labelsize=20)
plt.savefig('latency_involved.png', dpi=300)
plt.close(fig1)

# Graph 2 생성 및 저장 (왼쪽 여백 증가로 가운데 정렬)
fig2 = plt.figure(figsize=(10, 6))
ax2 = fig2.add_axes([left, bottom, width, height])
ax2.plot(x1, y2_matrix, marker='o', linestyle='-', color='blue', label='Matrix')
# ax2.plot(x, y2_baseline, marker='o', linestyle='-', color='red', label='CoChain')
# ax2.plot(x, y2_omni, marker='o', linestyle='-', color='black', label='OmniLedger')
ax2.set_xticks(x1)
ax2.set_yticks([0, 5, 10, 15, 20, 25, 30])
ax2.grid(False)
ax2.legend(loc=legend_loc, bbox_to_anchor=legend_bbox_to_anchor, frameon=False, fontsize=15)
ax2.set_xlabel('Number of involved shard', fontsize=20, labelpad=5)
ax2.set_ylabel('Number of messages sent', fontsize=20, labelpad=5)
ax2.tick_params(axis='both', which='major', labelsize=20)
plt.savefig('numberofmessage.png', dpi=300)
plt.close(fig2)

# Graph 3 생성 및 저장 (왼쪽 여백 증가로 가운데 정렬)
fig3 = plt.figure(figsize=(10, 6))
ax3 = fig3.add_axes([left, bottom, width, height])
ax3.plot(x2, y3_matrix, marker='o', linestyle='-', color='blue', label='Matrix')
ax3.plot(x2, y3_baseline, marker='o', linestyle='-', color='red', label='Baseline')
# ax3.plot(x, y3_omni, marker='o', linestyle='-', color='black', label='OmniLedger')
ax3.set_xticks(x2)
ax3.set_yticks([0, 1, 2, 3, 4, 5, 6])
ax3.set_ylim(bottom=0)
ax3.grid(False)
ax3.legend(loc=legend_loc, bbox_to_anchor=legend_bbox_to_anchor, frameon=False, fontsize=15)
ax3.set_xlabel('Injection rate (txn/s)', fontsize=20, labelpad=5)
ax3.set_ylabel('Latency (sec)', fontsize=20, labelpad=5)
ax3.tick_params(axis='both', which='major', labelsize=20)
plt.savefig('latency_injection.png', dpi=300)
plt.close(fig3)

# 필요 시 표시 (저장만 하는 경우 주석 처리 가능)
# plt.show()
