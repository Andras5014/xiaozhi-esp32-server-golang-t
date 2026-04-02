<template>
  <div class="admin-config">
    <div class="page-header">
      <h2>智能体配置管理</h2>
    </div>

    <el-card class="main-card" shadow="hover">
      <div class="toolbar">
        <div class="toolbar-right">
          <el-button
            type="warning"
            plain
            :loading="testingAll"
            @click="testAllConfigs"
            :disabled="!getEnabledConfigs().length"
          >
            测试全部
          </el-button>
          <el-button
            type="primary"
            @click="
              () => {
                resetForm();
                showDialog = true;
              }
            "
          >
            <el-icon><Plus /></el-icon>
            添加配置
          </el-button>
        </div>
      </div>

      <el-table
        :data="configs"
        v-loading="loading"
        stripe
        border
        class="config-table"
      >
        <el-table-column prop="id" label="ID" width="70" align="center" />
        <el-table-column prop="name" label="配置名称" />
        <el-table-column prop="config_id" label="配置ID" width="140" />
        <el-table-column prop="provider" label="类型" />
        <el-table-column
          prop="enabled"
          label="启用状态"
          width="90"
          align="center"
        >
          <template #default="scope">
            <el-switch
              v-model="scope.row.enabled"
              @change="toggleEnable(scope.row)"
            />
          </template>
        </el-table-column>
        <el-table-column prop="is_default" label="默认配置" align="center">
          <template #default="scope">
            <el-switch
              v-model="scope.row.is_default"
              @change="toggleDefault(scope.row)"
              :disabled="
                scope.row.is_default && getEnabledConfigs().length === 1
              "
            />
          </template>
        </el-table-column>
        <el-table-column label="测试结果" width="130" align="center">
          <template #default="scope">
            <template v-if="testResults[scope.row.config_id]">
              <el-tooltip
                v-if="testResults[scope.row.config_id].ok"
                :content="formatTestResultTip(testResults[scope.row.config_id])"
                placement="top"
              >
                <span class="test-result test-ok">{{
                  formatTestResultLabel(testResults[scope.row.config_id])
                }}</span>
              </el-tooltip>
              <el-tooltip
                v-else
                :content="testResults[scope.row.config_id].message"
                placement="top"
                :show-after="200"
              >
                <span class="test-result test-err">错误</span>
              </el-tooltip>
            </template>
            <span v-else class="test-result test-none">-</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="created_at"
          label="创建时间"
          width="170"
          align="center"
        >
          <template #default="scope">
            <span class="time-text">{{
              formatDate(scope.row.created_at)
            }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right" align="center">
          <template #default="scope">
            <el-button size="small" @click="editConfig(scope.row)"
              >编辑</el-button
            >
            <el-button
              size="small"
              type="warning"
              :loading="testingId === scope.row.config_id"
              @click="testConfig(scope.row, 'llm')"
            >
              测试
            </el-button>
            <el-button
              size="small"
              type="danger"
              @click="deleteConfig(scope.row.id)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 添加/编辑配置弹窗 -->
    <el-dialog
      v-model="showDialog"
      :title="editingConfig ? '编辑配置' : '添加配置'"
      width="600px"
      @close="handleDialogClose"
    >
      <LLMConfigForm
        ref="formRef"
        :model="form"
        :rules="rules"
        :visible="showDialog"
      />

      <template #footer>
        <el-button @click="handleDialogClose">取消</el-button>
        <el-button
          type="warning"
          plain
          @click="testCurrentConfig"
          :loading="testingCurrent"
        >
          测试
        </el-button>
        <el-button type="primary" @click="handleSave" :loading="saving">
          保存
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { Plus } from "@element-plus/icons-vue";
import api from "../../utils/api";
import {
  testSingleConfig,
  testWithData,
  parseJsonData,
} from "../../utils/configTest";
import LLMConfigForm from "./forms/LLMConfigForm.vue";

const configs = ref([]);
const testingId = ref(null);
const testingAll = ref(false);
const testingCurrent = ref(false);
const testResults = ref({}); // config_id -> { ok, message }
const loading = ref(false);
const saving = ref(false);
const showDialog = ref(false);
const editingConfig = ref(null);
const formRef = ref();

const form = reactive({
  name: "",
  config_id: "",
  provider: "weknora",
  is_default: false,
  enabled: true,
  type: "weknora",
  model_name: "",
  api_key: "",
  base_url: "",
  max_tokens: 4000,
  temperature: 0.7,
  top_p: 0.9,
  bot_id: "",
  user_prefix: "",
  connector_id: "1024",
});

const rules = {
  name: [{ required: true, message: "请输入配置名称", trigger: "blur" }],
  config_id: [{ required: true, message: "请输入配置ID", trigger: "blur" }],
  provider: [{ required: false, message: "请选择提供商", trigger: "change" }],
  type: [{ required: true, message: "请选择模型类型", trigger: "change" }],
  model_name: [
    {
      validator: (_, value, callback) => {
        if ((form.type === "openai" || form.type === "ollama") && !value) {
          callback(new Error("请输入模型名称"));
          return;
        }
        callback();
      },
      trigger: "blur",
    },
  ],
  api_key: [{ required: true, message: "请输入API密钥", trigger: "blur" }],
  base_url: [
    {
      validator: (_, value, callback) => {
        if (form.type !== "coze" && !value) {
          callback(new Error("请输入基础URL"));
          return;
        }
        callback();
      },
      trigger: "blur",
    },
  ],
  max_tokens: [
    {
      validator: (_, value, callback) => {
        if (
          (form.type === "openai" || form.type === "ollama") &&
          (!value || Number(value) < 1 || Number(value) > 100000)
        ) {
          callback(new Error("max_tokens必须在1-100000之间"));
          return;
        }
        callback();
      },
      trigger: "blur",
    },
  ],
  bot_id: [
    {
      validator: (_, value, callback) => {
        if (form.type === "coze" && !value) {
          callback(new Error("请输入Coze Bot ID"));
          return;
        }
        callback();
      },
      trigger: "blur",
    },
  ],
  temperature: [
    {
      type: "number",
      min: 0,
      max: 2,
      message: "温度必须在0-2之间",
      trigger: "blur",
    },
  ],
  top_p: [
    {
      type: "number",
      min: 0,
      max: 1,
      message: "Top P必须在0-1之间",
      trigger: "blur",
    },
  ],
};

const loadConfigs = async () => {
  loading.value = true;
  try {
    const response = await api.get("/admin/llm-configs");
    configs.value = response.data.data || [];
  } catch (error) {
    ElMessage.error("加载配置失败");
  } finally {
    loading.value = false;
  }
};

const editConfig = (config) => {
  editingConfig.value = config;
  form.name = config.name;
  form.config_id = config.config_id;
  form.provider = config.provider;
  form.is_default = config.is_default;
  form.enabled = config.enabled;

  // 解析配置JSON并填充到对应字段
  try {
    const configObj = JSON.parse(config.json_data || "{}");
    const detectedType =
      configObj.type ||
      (config.provider === "coze"
        ? "coze"
        : config.provider === "dify"
          ? "dify"
          : "openai");
    form.type = detectedType;
    form.model_name =
      configObj.model_name ||
      (detectedType === "coze"
        ? "coze"
        : detectedType === "dify"
          ? "dify"
          : "");
    form.api_key = configObj.api_key || "";
    form.base_url =
      configObj.base_url ||
      (detectedType === "coze"
        ? "https://api.coze.com"
        : detectedType === "dify"
          ? "https://api.dify.ai/v1"
          : "");
    form.max_tokens = configObj.max_tokens || 4000;
    form.temperature = configObj.temperature || 0.7;
    form.top_p = configObj.top_p || 0.9;
    form.bot_id = configObj.bot_id || "";
    form.user_prefix = configObj.user_prefix || "";
    form.connector_id = configObj.connector_id || "1024";
    form.web_search_enabled = configObj.web_search_enabled || false;
    form.agent_id = configObj.agent_id || "";
  } catch (error) {
    console.error("解析配置JSON失败:", error);
  }

  showDialog.value = true;
};

const handleSave = async () => {
  if (!formRef.value) return;

  await formRef.value.validate(async (valid) => {
    if (valid) {
      saving.value = true;
      try {
        // 检查是否是首次添加配置
        const isFirstConfig =
          !editingConfig.value && configs.value.length === 0;

        const configData = {
          name: form.name,
          config_id: form.config_id,
          provider: form.provider,
          is_default: isFirstConfig || form.is_default, // 首次添加自动设为默认
          enabled: form.enabled !== undefined ? form.enabled : true,
          json_data: formRef.value.getJsonData(),
        };

        if (editingConfig.value) {
          await api.put(
            `/admin/llm-configs/${editingConfig.value.id}`,
            configData,
          );
          ElMessage.success("配置更新成功");
        } else {
          await api.post("/admin/llm-configs", configData);
          ElMessage.success("配置创建成功");
        }

        showDialog.value = false;
        loadConfigs();
      } catch (error) {
        ElMessage.error(
          "保存失败: " + (error.response?.data?.message || error.message),
        );
      } finally {
        saving.value = false;
      }
    }
  });
};

const toggleEnable = async (config) => {
  try {
    await api.post(`/admin/configs/${config.id}/toggle`);
    ElMessage.success(`${config.enabled ? "启用" : "禁用"}成功`);
  } catch (error) {
    // 恢复开关状态
    config.enabled = !config.enabled;
    ElMessage.error("操作失败");
  }
};

const toggleDefault = async (config) => {
  try {
    if (!config.enabled) {
      ElMessage.warning("请先启用该配置才能设为默认");
      config.is_default = false;
      return;
    }

    const configData = {
      name: config.name,
      config_id: config.config_id,
      provider: config.provider,
      is_default: config.is_default,
      enabled: config.enabled,
      json_data: config.json_data,
    };

    await api.put(`/admin/llm-configs/${config.id}`, configData);
    ElMessage.success(config.is_default ? "设为默认成功" : "取消默认成功");

    // 刷新列表以更新其他配置的默认状态
    loadConfigs();
  } catch (error) {
    // 恢复开关状态
    config.is_default = !config.is_default;
    ElMessage.error("操作失败");
  }
};

const getEnabledConfigs = () => {
  return configs.value.filter((config) => config.enabled);
};

function formatTestResultLabel(r) {
  if (!r?.ok) return "错误";
  return r.first_packet_ms != null ? `正确 ${r.first_packet_ms}ms` : "正确";
}
function formatTestResultTip(r) {
  if (!r?.ok) return "";
  return r.first_packet_ms != null
    ? `通过，耗时 ${r.first_packet_ms}ms`
    : "通过";
}
function formatTestMessage(result) {
  const base = result.message || "";
  return result.first_packet_ms != null
    ? `${base} ${result.first_packet_ms}ms`
    : base;
}

const testConfig = async (row, type) => {
  testingId.value = row.config_id;
  try {
    const result = await testSingleConfig(type, row.config_id);
    testResults.value = { ...testResults.value, [row.config_id]: result };
    if (result.ok) {
      ElMessage.success(
        `${row.name || row.config_id}：${formatTestMessage(result)}`,
      );
    } else {
      ElMessage.warning(`${row.name || row.config_id}：${result.message}`);
    }
  } catch (err) {
    ElMessage.error(err.response?.data?.error || "测试请求失败");
  } finally {
    testingId.value = null;
  }
};

const testAllConfigs = async () => {
  const list = getEnabledConfigs();
  if (!list.length) {
    ElMessage.warning("没有已启用的配置");
    return;
  }
  testingAll.value = true;
  testResults.value = {};
  let okCount = 0;
  try {
    for (const row of list) {
      try {
        const result = await testSingleConfig("llm", row.config_id);
        testResults.value = { ...testResults.value, [row.config_id]: result };
        if (result.ok) okCount++;
      } catch (_) {
        testResults.value = {
          ...testResults.value,
          [row.config_id]: { ok: false, message: "请求失败" },
        };
      }
    }
    ElMessage.success(`全部测试完成：${okCount}/${list.length} 通过`);
  } catch (err) {
    ElMessage.error(err.response?.data?.error || "测试请求失败");
  } finally {
    testingAll.value = false;
  }
};

const testCurrentConfig = async () => {
  if (!formRef.value) return;
  try {
    await formRef.value.validate();
  } catch (_) {
    return;
  }
  const configId = form.config_id?.trim();
  if (!configId) {
    ElMessage.warning("请填写配置ID");
    return;
  }
  const payload = {
    name: form.name,
    config_id: configId,
    provider: form.provider,
    is_default: form.is_default,
    ...parseJsonData(formRef.value.getJsonData()),
  };
  testingCurrent.value = true;
  try {
    const result = await testWithData("llm", { [configId]: payload });
    if (result.ok) {
      ElMessage.success(formatTestMessage(result) || "测试通过");
    } else {
      ElMessage.warning(result.message || "测试未通过");
    }
  } catch (err) {
    ElMessage.error(err.response?.data?.error || "测试请求失败");
  } finally {
    testingCurrent.value = false;
  }
};

const deleteConfig = async (id) => {
  try {
    await ElMessageBox.confirm("确定要删除这个配置吗？", "提示", {
      confirmButtonText: "确定",
      cancelButtonText: "取消",
      type: "warning",
    });

    await api.delete(`/admin/llm-configs/${id}`);
    ElMessage.success("删除成功");
    loadConfigs();
  } catch (error) {
    if (error !== "cancel") {
      ElMessage.error("删除失败");
    }
  }
};

const resetForm = () => {
  editingConfig.value = null;
  form.name = "";
  form.config_id = "";
  form.provider = "weknora";
  form.is_default = false;
  form.enabled = true;
  form.type = "weknora";
  form.model_name = "";
  form.api_key = "";
  form.base_url = "";
  form.max_tokens = 4000;
  form.temperature = 0.7;
  form.top_p = 0.9;
  form.bot_id = "";
  form.user_prefix = "";
  form.connector_id = "1024";
};

const handleDialogClose = () => {
  showDialog.value = false;
  resetForm();
  if (formRef.value) {
    formRef.value.resetFields();
  }
};

const formatDate = (dateString) => {
  return new Date(dateString).toLocaleString("zh-CN");
};

onMounted(() => {
  loadConfigs();
});
</script>

<style scoped>
.admin-config {
  padding: 24px;
}

.page-header {
  margin-bottom: 24px;
}

.page-header h2 {
  margin: 0;
  color: #1f2937;
  font-size: 28px;
  font-weight: 600;
  letter-spacing: -0.025em;
}

.main-card {
  border-radius: 16px;
}

.toolbar {
  margin-bottom: 24px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 16px;
}

.toolbar-right {
  display: flex;
  align-items: center;
}

.config-table {
  border-radius: 12px;
  overflow: hidden;
}

.time-text {
  color: #6b7280;
  font-size: 12px;
}

.test-result {
  font-size: 12px;
  font-weight: 500;
}
.test-result.test-ok {
  color: #10b981;
}
.test-result.test-err {
  color: #ef4444;
  cursor: help;
}
.test-result.test-none {
  color: #9ca3af;
}

:deep(.el-table .el-table__header-wrapper th) {
  background-color: #f9fafb;
  color: #374151;
  font-weight: 700;
  height: 50px;
}

:deep(.el-table .el-table__row) {
  height: 60px;
}
</style>
