<template>
  <div class="admin-config">
    <div class="page-header">
      <h2>资源池统计</h2>
      <div class="header-actions">
        <el-button type="primary" @click="refreshStats">
          <el-icon><Refresh /></el-icon>
          刷新
        </el-button>
        <el-select
          v-model="viewType"
          style="width: 120px; margin-left: 10px"
          disabled
        >
          <el-option label="最新数据" value="latest" />
        </el-select>
      </div>
    </div>

    <el-card class="config-card" shadow="never">
      <!-- 统计摘要 -->
      <el-row :gutter="24" style="margin-bottom: 32px">
        <el-col :xs="24" :sm="12" :md="6">
          <div class="stat-item">
            <div class="stat-title">总记录数</div>
            <div class="stat-value">{{ summary.total_records || 0 }}</div>
          </div>
        </el-col>
        <el-col :xs="24" :sm="12" :md="6">
          <div class="stat-item">
            <div class="stat-title">存储方式</div>
            <div class="stat-value" style="font-size: 18px">仅最新数据</div>
          </div>
        </el-col>
        <el-col :xs="24" :sm="12" :md="6">
          <div class="stat-item">
            <div class="stat-title">最早时间</div>
            <div class="stat-value" style="font-size: 16px">
              {{ formatTime(summary.oldest_timestamp) }}
            </div>
          </div>
        </el-col>
        <el-col :xs="24" :sm="12" :md="6">
          <div class="stat-item">
            <div class="stat-title">最新时间</div>
            <div class="stat-value" style="font-size: 16px">
              {{ formatTime(summary.newest_timestamp) }}
            </div>
          </div>
        </el-col>
      </el-row>

      <!-- 最新统计数据 -->
      <div v-if="viewType === 'latest' && latestStats">
        <h3 class="section-title">
          最新统计数据（{{ formatTime(latestStats.timestamp) }}）
        </h3>
        <el-table
          :data="formatStatsData(latestStats.stats)"
          stripe
          style="width: 100%"
          v-if="latestStats.stats"
        >
          <el-table-column
            prop="poolKey"
            label="资源池"
            min-width="200"
            show-overflow-tooltip
          />
          <el-table-column
            prop="total"
            label="总资源数"
            width="140"
            align="center"
          />
          <el-table-column
            prop="available"
            label="可用资源"
            width="140"
            align="center"
          />
          <el-table-column
            prop="inUse"
            label="使用中"
            width="140"
            align="center"
          />
          <el-table-column
            prop="maxSize"
            label="最大容量"
            width="140"
            align="center"
          />
          <el-table-column
            prop="minSize"
            label="最小容量"
            width="140"
            align="center"
          />
          <el-table-column
            prop="maxIdle"
            label="最大空闲"
            width="140"
            align="center"
          />
          <el-table-column
            prop="isClosed"
            label="状态"
            width="140"
            fixed="right"
            align="center"
          >
            <template #default="{ row }">
              <el-tag :type="row.isClosed ? 'danger' : 'success'" size="small">
                {{ row.isClosed ? "已关闭" : "运行中" }}
              </el-tag>
            </template>
          </el-table-column>
        </el-table>
      </div>

      <!-- 空状态 -->
      <el-empty v-if="!latestStats" description="暂无统计数据" />
    </el-card>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from "vue";
import api from "@/utils/api";
import { ElMessage } from "element-plus";
import { Refresh } from "@element-plus/icons-vue";

const viewType = ref("latest");
const latestStats = ref(null);
const summary = ref({
  total_records: 0,
  storage_duration: "仅保存最新数据",
  oldest_timestamp: null,
  newest_timestamp: null,
});

let refreshTimer = null;

onMounted(() => {
  loadSummary();
  loadStats();
  refreshTimer = setInterval(() => {
    loadStats();
  }, 30000);
});

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer);
  }
});

const loadSummary = async () => {
  try {
    const response = await api.get("/admin/pool/stats/summary");
    summary.value = response.data?.data || {};
  } catch (error) {
    console.error("加载统计摘要失败:", error);
  }
};

const loadStats = async () => {
  try {
    const response = await api.get("/admin/pool/stats?type=latest");
    latestStats.value = response.data?.data || response.data || null;
  } catch (error) {
    console.error("加载统计数据失败:", error);
    ElMessage.error("加载统计数据失败");
  }
};

const refreshStats = () => {
  loadSummary();
  loadStats();
  ElMessage.success("刷新成功");
};

const formatStatsData = (stats) => {
  if (!stats || typeof stats !== "object") {
    return [];
  }

  const result = [];
  for (const [poolKey, poolStats] of Object.entries(stats)) {
    if (poolStats && typeof poolStats === "object") {
      result.push({
        poolKey,
        total: poolStats.total_resources || 0,
        available: poolStats.available_resources || 0,
        inUse: poolStats.in_use_resources || 0,
        maxSize: poolStats.max_size || 0,
        minSize: poolStats.min_size || 0,
        maxIdle: poolStats.max_idle || 0,
        isClosed: poolStats.is_closed || false,
      });
    }
  }
  return result;
};

const formatTime = (timestamp) => {
  if (!timestamp) {
    return "-";
  }
  const date = new Date(timestamp);
  return date.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
};
</script>

<style scoped>
.admin-config {
  padding: 24px;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24px;
}

.page-header h2 {
  margin: 0;
  color: #1f2937;
  font-size: 28px;
  font-weight: 600;
  letter-spacing: -0.025em;
}

.header-actions {
  display: flex;
  align-items: center;
}

.config-card {
  border-radius: 16px;
  border: 1px solid #e5e7eb;
}

:deep(.el-card__body) {
  padding: 24px;
}

.stat-item {
  text-align: center;
  padding: 16px;
  background: #f9fafb;
  border-radius: 12px;
  transition: all 0.3s ease;
  height: 100%;
  display: flex;
  flex-direction: column;
  justify-content: center;
}

.stat-item:hover {
  background: #f3f4f6;
  transform: translateY(-2px);
}

.stat-title {
  font-size: 14px;
  color: #6b7280;
  margin-bottom: 8px;
  font-weight: 500;
}

.stat-value {
  font-size: 24px;
  font-weight: 700;
  color: #111827;
}

.section-title {
  font-size: 16px;
  font-weight: 700;
  color: #374151;
  margin: 16px 0 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid #f1f5f9;
}

:deep(.el-table .el-table__header-wrapper th) {
  background-color: #f9fafb;
  color: #374151;
  font-weight: 700;
  height: 50px;
}

@media (max-width: 992px) {
  .el-col {
    margin-bottom: 20px;
  }
}
</style>
