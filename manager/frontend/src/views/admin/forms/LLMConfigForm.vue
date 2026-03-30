<template>
  <el-form ref="formRef" :model="model" :rules="rules" label-width="120px">
    <el-form-item label="提供商" prop="provider">
      <el-select v-model="model.provider" placeholder="请选择提供商" style="width: 100%" @change="onProviderChange">
        <el-option label="OpenAI" value="openai" />
        <el-option label="Azure OpenAI" value="azure" />
        <el-option label="Anthropic" value="anthropic" />
        <el-option label="智谱AI" value="zhipu" />
        <el-option label="阿里云" value="aliyun" />
        <el-option label="豆包" value="doubao" />
        <el-option label="SiliconFlow" value="siliconflow" />
        <el-option label="DeepSeek" value="deepseek" />
        <el-option label="Dify" value="dify" />
        <el-option label="Coze" value="coze" />
        <el-option label="WeKnora" value="weknora" />
      </el-select>
    </el-form-item>
    <el-form-item label="配置名称" prop="name">
      <el-input v-model="model.name" placeholder="请输入配置名称" />
    </el-form-item>
    <el-form-item label="配置ID" prop="config_id">
      <el-input v-model="model.config_id" placeholder="请输入唯一的配置ID" />
    </el-form-item>
    <el-form-item label="模型类型" prop="type">
      <el-select v-model="model.type" placeholder="请选择模型类型" style="width: 100%" @change="onTypeChange">
        <el-option label="OpenAI" value="openai" />
        <el-option label="Ollama" value="ollama" />
        <el-option label="Dify" value="dify" />
        <el-option label="Coze" value="coze" />
        <el-option label="WeKnora" value="weknora" />
      </el-select>
    </el-form-item>

    <el-form-item v-if="isOpenAIOrOllama" label="模型名称" prop="model_name">
      <el-input v-model="model.model_name" placeholder="请输入模型名称" />
    </el-form-item>

    <el-form-item label="API密钥" prop="api_key">
      <el-input v-model="model.api_key" type="password" placeholder="请输入API密钥" show-password />
    </el-form-item>

    <el-form-item v-if="showBaseURL" label="基础URL" prop="base_url">
      <el-input v-model="model.base_url" :placeholder="isWeknora ? 'http://IP:8080（不含 /api/v1）' : '请输入基础URL'" style="width: 100%" />
    </el-form-item>

    <el-form-item v-if="isCoze" label="Bot ID" prop="bot_id">
      <el-input v-model="model.bot_id" placeholder="请输入 Coze Bot ID" />
    </el-form-item>

    <el-form-item v-if="isDify || isCoze || isWeknora" label="User前缀" prop="user_prefix">
      <el-input v-model="model.user_prefix" placeholder="可选，默认 xiaozhi" />
    </el-form-item>

    <el-form-item v-if="isCoze" label="Connector ID" prop="connector_id">
      <el-input v-model="model.connector_id" placeholder="可选，默认 1024" />
    </el-form-item>

    <el-form-item v-if="isWeknora" label="知识库ID" prop="knowledge_base_id">
      <el-input v-model="model.knowledge_base_id" placeholder="可选，WeKnora 知识库 ID" />
    </el-form-item>

    <el-form-item v-if="isWeknora" label="智能体" prop="agent_id">
      <div style="display: flex; gap: 8px; width: 100%">
        <el-select
          v-model="model.agent_id"
          placeholder="请先加载智能体列表"
          style="flex: 1"
          clearable
          :loading="weknoraAgentLoading"
        >
          <el-option
            v-for="agent in weknoraAgents"
            :key="agent.id"
            :label="`${agent.name}${agent.agent_mode ? ' (' + agent.agent_mode + ')' : ''}`"
            :value="agent.id"
          />
        </el-select>
        <el-button :loading="weknoraAgentLoading" @click="fetchWeknoraAgents">加载</el-button>
      </div>
      <div v-if="weknoraAgentError" style="color: var(--el-color-danger); font-size: 12px; margin-top: 4px">{{ weknoraAgentError }}</div>
    </el-form-item>

    <el-form-item v-if="isWeknora" label="网络搜索" prop="web_search_enabled">
      <el-switch v-model="model.web_search_enabled" />
    </el-form-item>

    <el-form-item v-if="isOpenAIOrOllama" label="max_tokens" prop="max_tokens">
      <el-input-number v-model="model.max_tokens" :min="1" :max="100000" placeholder="max_tokens" style="width: 100%" />
    </el-form-item>

    <el-form-item v-if="isOpenAIOrOllama" label="温度" prop="temperature">
      <el-input-number v-model="model.temperature" :min="0" :max="2" :step="0.1" placeholder="温度" style="width: 100%" />
    </el-form-item>

    <el-form-item v-if="isOpenAIOrOllama" label="Top P" prop="top_p">
      <el-input-number v-model="model.top_p" :min="0" :max="1" :step="0.1" placeholder="Top P" style="width: 100%" />
    </el-form-item>
  </el-form>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import api from '@/utils/api'

const quickUrls = {
  openai: 'https://api.openai.com/v1',
  azure: 'https://your-resource-name.openai.azure.com',
  anthropic: 'https://api.anthropic.com',
  zhipu: 'https://open.bigmodel.cn/api/paas/v4',
  aliyun: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
  doubao: 'https://ark.cn-beijing.volces.com/api/v3',
  siliconflow: 'https://api.siliconflow.cn/v1',
  deepseek: 'https://api.deepseek.com/v1',
  dify: 'https://api.dify.ai/v1',
  weknora: 'http://127.0.0.1:8080'
}

const props = defineProps({
  model: { type: Object, required: true },
  rules: { type: Object, default: () => ({}) }
})

const formRef = ref()
const providerTypeMap = {
  dify: 'dify',
  coze: 'coze',
  weknora: 'weknora'
}

const isOpenAIOrOllama = computed(() => props.model?.type === 'openai' || props.model?.type === 'ollama')
const isDify = computed(() => props.model?.type === 'dify')
const isCoze = computed(() => props.model?.type === 'coze')
const isWeknora = computed(() => props.model?.type === 'weknora')
const showBaseURL = computed(() => true)

const weknoraAgents = ref([])
const weknoraAgentLoading = ref(false)
const weknoraAgentError = ref('')

async function fetchWeknoraAgents() {
  const baseURL = String(props.model?.base_url || '').trim()
  const apiKey = String(props.model?.api_key || '').trim()
  if (!baseURL || !apiKey) {
    weknoraAgentError.value = '请先填写基础URL和API密钥'
    return
  }
  weknoraAgentLoading.value = true
  weknoraAgentError.value = ''
  try {
    const res = await api.post('/admin/llm-configs/weknora/agents', {
      base_url: baseURL,
      api_key: apiKey
    })
    weknoraAgents.value = res.data?.data || []
    if (weknoraAgents.value.length === 0) {
      weknoraAgentError.value = 'WeKnora 上暂无可用智能体'
    }
  } catch (e) {
    weknoraAgentError.value = e.response?.data?.error || e.message || '加载失败'
    weknoraAgents.value = []
  } finally {
    weknoraAgentLoading.value = false
  }
}

watch(() => props.model?.type, (value) => {
  if (!value || !props.model) {
    return
  }
  if (value === 'dify') {
    props.model.provider = 'dify'
    if (!props.model.base_url) {
      props.model.base_url = quickUrls.dify
    }
    if (!props.model.model_name) {
      props.model.model_name = 'dify'
    }
  }
  if (value === 'coze') {
    props.model.provider = 'coze'
    if (!props.model.base_url) {
      props.model.base_url = 'https://api.coze.com'
    }
    if (!props.model.model_name) {
      props.model.model_name = 'coze'
    }
    if (!props.model.connector_id) {
      props.model.connector_id = '1024'
    }
  }
  if (value === 'weknora') {
    props.model.provider = 'weknora'
    if (!props.model.base_url) {
      props.model.base_url = quickUrls.weknora
    }
    if (!props.model.model_name) {
      props.model.model_name = 'weknora'
    }
  }
})

function onProviderChange(value) {
  if (!value || !props.model) {
    return
  }
  if (providerTypeMap[value]) {
    props.model.type = providerTypeMap[value]
  }
  if (quickUrls[value]) {
    props.model.base_url = quickUrls[value]
  }
}

function onTypeChange(value) {
  if (!props.model || !value) {
    return
  }
  if (value === 'dify') {
    props.model.provider = 'dify'
    if (!props.model.base_url) {
      props.model.base_url = quickUrls.dify
    }
  }
  if (value === 'coze') {
    props.model.provider = 'coze'
    if (!props.model.base_url) {
      props.model.base_url = 'https://api.coze.com'
    }
  }
  if (value === 'weknora') {
    props.model.provider = 'weknora'
    if (!props.model.base_url) {
      props.model.base_url = quickUrls.weknora
    }
  }
  if (value === 'openai' && !props.model.base_url) {
    props.model.base_url = quickUrls.openai
  }
}

function getJsonData() {
  const m = props.model
  if (m.type === 'dify') {
    const config = {
      type: 'dify',
      api_key: m.api_key,
      base_url: m.base_url,
      user_prefix: m.user_prefix
    }
    return JSON.stringify(config, null, 2)
  }
  if (m.type === 'coze') {
    const config = {
      type: 'coze',
      api_key: m.api_key,
      base_url: m.base_url,
      bot_id: m.bot_id,
      user_prefix: m.user_prefix,
      connector_id: m.connector_id
    }
    return JSON.stringify(config, null, 2)
  }
  if (m.type === 'weknora') {
    const config = {
      type: 'weknora',
      api_key: m.api_key,
      base_url: m.base_url,
      user_prefix: m.user_prefix,
      knowledge_base_id: m.knowledge_base_id,
      agent_id: m.agent_id,
      agent_enabled: true,
      web_search_enabled: !!m.web_search_enabled
    }
    return JSON.stringify(config, null, 2)
  }

  const config = {
    type: m.type,
    model_name: m.model_name,
    api_key: m.api_key,
    base_url: m.base_url,
    max_tokens: m.max_tokens
  }
  if (m.temperature !== undefined && m.temperature !== null) config.temperature = m.temperature
  if (m.top_p !== undefined && m.top_p !== null) config.top_p = m.top_p
  return JSON.stringify(config, null, 2)
}

function validate(callback) {
  if (callback) {
    return formRef.value?.validate((valid) => {
      let finalValid = valid
      if (finalValid && isCoze.value && !props.model?.bot_id) {
        finalValid = false
      }
      callback(finalValid)
      return finalValid
    })
  }

  return formRef.value?.validate().then(() => {
    if (isCoze.value && !props.model?.bot_id) {
      return Promise.reject(new Error('请输入Coze Bot ID'))
    }
    return true
  })
}

function resetFields() {
  formRef.value?.resetFields()
}

defineExpose({ validate, getJsonData, resetFields })
</script>
