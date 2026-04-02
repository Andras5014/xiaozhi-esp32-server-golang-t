<template>
  <!-- 桌面端布局：使用Element Plus -->
  <el-container v-if="!isMobileDevice" class="layout-container">
    <el-aside width="240px" class="sidebar">
      <div class="logo">
        <h3>布法罗智能体管理系统</h3>
      </div>
      <el-menu
        :default-active="$route.path"
        class="sidebar-menu"
        router
        background-color="#0F172B"
        text-color="#bfcbd9"
      >
        <el-menu-item index="/dashboard">
          <el-icon><House /></el-icon>
          <span>仪表板</span>
        </el-menu-item>

        <el-menu-item v-if="!authStore.isAdmin" index="/console">
          <el-icon><Monitor /></el-icon>
          <span>用户控制台</span>
        </el-menu-item>

        <el-menu-item v-if="!authStore.isAdmin" index="/agents">
          <el-icon><Monitor /></el-icon>
          <span>智能体管理</span>
        </el-menu-item>

        <el-menu-item v-if="!authStore.isAdmin" index="/user/roles">
          <el-icon><User /></el-icon>
          <span>我的角色</span>
        </el-menu-item>

        <el-menu-item v-if="!authStore.isAdmin" index="/user/api-tokens">
          <el-icon><Key /></el-icon>
          <span>API Token</span>
        </el-menu-item>

        <el-menu-item v-if="!authStore.isAdmin" index="/speakers">
          <el-icon><Microphone /></el-icon>
          <span>声纹管理</span>
        </el-menu-item>
        <el-menu-item v-if="!authStore.isAdmin" index="/voice-clones">
          <el-icon><Microphone /></el-icon>
          <span>声音复刻</span>
        </el-menu-item>

        <el-menu-item v-if="!authStore.isAdmin" index="/user/knowledge-bases">
          <el-icon><Document /></el-icon>
          <span>我的知识库</span>
        </el-menu-item>

        <!-- 服务配置 -->
        <el-sub-menu v-if="authStore.isAdmin" index="/admin/service-config">
          <template #title>
            <el-icon><Tools /></el-icon>
            <span>服务配置</span>
          </template>
          <el-menu-item index="/admin/ota-config">OTA配置</el-menu-item>
          <el-menu-item index="/admin/mqtt-config">MQTT配置</el-menu-item>
          <el-menu-item index="/admin/mqtt-server-config"
            >MQTT Server配置</el-menu-item
          >
          <el-menu-item index="/admin/udp-config">UDP配置</el-menu-item>
          <el-sub-menu index="/admin/mcp-config-group">
            <template #title>MCP配置</template>
            <el-menu-item index="/admin/mcp-config">配置</el-menu-item>
            <el-menu-item index="/admin/mcp-market">MCP市场</el-menu-item>
          </el-sub-menu>
          <el-menu-item index="/admin/speaker-config"
            >声纹识别配置</el-menu-item
          >
          <el-menu-item index="/admin/chat-settings">聊天设置</el-menu-item>
        </el-sub-menu>

        <!-- AI配置 -->
        <el-sub-menu v-if="authStore.isAdmin" index="/admin/ai-config">
          <template #title>
            <el-icon><Cpu /></el-icon>
            <span>AI配置</span>
          </template>
          <el-menu-item index="/admin/vad-config">VAD配置</el-menu-item>
          <el-menu-item index="/admin/asr-config">ASR配置</el-menu-item>
          <el-menu-item index="/admin/tts-config">TTS配置</el-menu-item>
          <el-menu-item index="/admin/llm-config">智能体配置</el-menu-item>
          <!-- <el-menu-item index="/admin/vision-config">Vision配置</el-menu-item> -->
          <!-- <el-menu-item index="/admin/memory-config">Memory配置</el-menu-item> -->
          <!-- <el-menu-item index="/admin/knowledge-search-config">
            知识库检索配置
          </el-menu-item> -->
        </el-sub-menu>

        <el-menu-item v-if="authStore.isAdmin" index="/voice-clones">
          <el-icon><Microphone /></el-icon>
          <span>声音复刻</span>
        </el-menu-item>

        <!-- 系统监控 -->
        <el-menu-item v-if="authStore.isAdmin" index="/admin/pool-stats">
          <el-icon><DataAnalysis /></el-icon>
          <span>资源池统计</span>
        </el-menu-item>

        <!-- 系统管理 -->
        <el-menu-item v-if="authStore.isAdmin" index="/admin/global-roles">
          <el-icon><tickets /></el-icon>
          <span>全局角色</span>
        </el-menu-item>

        <el-menu-item v-if="authStore.isAdmin" index="/admin/users">
          <el-icon><UserFilled /></el-icon>
          <span>用户管理</span>
        </el-menu-item>

        <el-menu-item v-if="authStore.isAdmin" index="/admin/devices">
          <el-icon><Iphone /></el-icon>
          <span>设备管理</span>
        </el-menu-item>

        <el-menu-item v-if="authStore.isAdmin" index="/admin/agents">
          <el-icon><Connection /></el-icon>
          <span>智能体管理</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header class="header">
        <div class="header-left">
          <span class="page-title">{{ currentPageTitle }}</span>
          <template v-if="authStore.isAdmin">
            <div class="header-nav-divider" />
            <router-link
              to="/admin/config-wizard"
              custom
              v-slot="{ navigate, isActive }"
            >
              <el-button
                :type="isActive ? 'primary' : 'default'"
                plain
                class="header-nav-btn"
                @click="navigate"
              >
                <el-icon class="header-nav-icon"><Guide /></el-icon>
                <span>配置向导</span>
              </el-button>
            </router-link>
            <router-link
              to="/admin/ota-config"
              custom
              v-slot="{ navigate, isActive }"
            >
              <el-button
                :type="isActive ? 'primary' : 'default'"
                plain
                class="header-nav-btn"
                @click="navigate"
              >
                <el-icon class="header-nav-icon"><Upload /></el-icon>
                <span>OTA配置</span>
              </el-button>
            </router-link>
          </template>
        </div>
        <div class="header-right">
          <el-dropdown @command="handleCommand" trigger="hover">
            <div class="user-profile-trigger">
              <img :src="Avatar" class="user-avatar" />
              <div class="user-info-text">
                <span class="username">{{ authStore.user?.username }}</span>
              </div>
            </div>
            <template #dropdown>
              <el-dropdown-menu class="premium-dropdown">
                <el-dropdown-item command="logout">
                  <el-icon><SwitchButton /></el-icon>
                  退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="main-content">
        <router-view />
      </el-main>
    </el-container>
  </el-container>

  <!-- 移动端布局：使用Vant组件 -->
  <MobileLayout v-else />
</template>

<script setup>
import { computed } from "vue";
import { useRouter, useRoute } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { useAuthStore } from "../stores/auth";
import { isMobile } from "../utils/device";
import MobileLayout from "./MobileLayout.vue";
import {
  House,
  Monitor,
  Setting,
  User,
  Key,
  ArrowDown,
  Tools,
  Cpu,
  UserFilled,
  Iphone,
  Connection,
  Microphone,
  DataAnalysis,
  Guide,
  Upload,
  Document,
  Tickets,
  SwitchButton,
} from "@element-plus/icons-vue";

import Avatar from "./avatar.png";

const router = useRouter();
const route = useRoute();
const authStore = useAuthStore();

// 设备检测
const isMobileDevice = computed(() => isMobile());

const currentPageTitle = computed(() => {
  return route.meta?.title || "仪表板";
});

const handleCommand = async (command) => {
  if (command === "logout") {
    try {
      await ElMessageBox.confirm("确定要退出登录吗？", "提示", {
        confirmButtonText: "确定",
        cancelButtonText: "取消",
        type: "warning",
        customClass: "logout-dialog",
      });

      authStore.logout();
      ElMessage.success("已退出登录");
      router.push("/login");
    } catch {
      // 用户取消
    }
  }
};
</script>

<style scoped>
.layout-container {
  height: 100vh;
}

.sidebar {
  background-color: #1e222d;
  overflow: hidden;
  box-shadow: 2px 0 8px rgba(0, 0, 0, 0.15);
}

.logo {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: #0f172b;
  color: white;
  margin-bottom: 0;
}

.logo h3 {
  margin: 0;
  font-size: 20px;
}

.sidebar-menu {
  border: none;
  height: calc(100vh - 60px);
  overflow-y: auto;
  padding: 6px 0;
}

/* 侧边栏菜单 Hover/Active 背景色及圆角 */
:deep(.sidebar-menu .el-menu-item),
:deep(.sidebar-menu .el-sub-menu__title) {
  margin: 4px 8px !important;
  border-radius: 8px !important;
  height: 46px !important;
  line-height: 46px !important;
}

:deep(.sidebar-menu .el-menu-item:hover),
:deep(.sidebar-menu .el-sub-menu__title:hover) {
  background-color: #242c42 !important;
}

:deep(.sidebar-menu .el-menu-item.is-active),
:deep(.sidebar-menu .el-sub-menu .el-menu-item.is-active) {
  background-color: #242c42 !important;
}

/* 侧边栏滚动条美化 */
.sidebar-menu::-webkit-scrollbar {
  width: 6px;
}

.sidebar-menu::-webkit-scrollbar-track {
  background: transparent;
}

.sidebar-menu::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.1);
  border-radius: 10px;
}

.sidebar-menu::-webkit-scrollbar-thumb:hover {
  background: rgba(255, 255, 255, 0.2);
}

.header {
  background-color: #fff;
  border-bottom: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  height: 64px !important;
  box-shadow: 0 1px 4px rgba(0, 21, 41, 0.08);
  z-index: 9;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 16px;
}

.header-left .page-title {
  font-size: 18px;
  font-weight: 600;
  color: #1f2937;
}

.header-nav-divider {
  width: 1px;
  height: 20px;
  background-color: #f0f0f0;
  margin: 0 8px;
}

.header-nav-btn {
  border-radius: 6px;
  padding: 6px 16px;
  font-weight: 500;
  border: 1px solid #e5e7eb;
  transition: all 0.2s;
}

.header-nav-btn:hover {
  background-color: #f9fafb;
  border-color: #d1d5db;
}

.header-right .user-profile-trigger {
  height: 64px;
  padding: 0 10px;
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
}

.header-right .user-profile-trigger:hover {
  background-color: #f5f5f5;
}

.user-avatar {
  width: 30px;
  height: 30px;
  border-radius: 50%;
}

.user-info-text {
  display: flex;
  flex-direction: column;
  line-height: 1.2;
}

.username {
  font-size: 16px;
}

.drop-icon {
  font-size: 14px;
  color: #94a3b8;
  margin-left: 0px;
}

/* Premium Dropdown Styling */
.premium-dropdown {
  padding: 4px !important;
}

.dropdown-user-header {
  padding: 12px 16px;
  border-bottom: 1px solid #f1f5f9;
  margin-bottom: 4px;
}

:deep(.premium-dropdown .el-dropdown-menu__item) {
  padding: 10px 16px !important;
  border-radius: 8px !important;
  margin: 2px 4px !important;
  font-size: 14px !important;
  color: #475569 !important;
  transition: all 0.2s !important;
}

:deep(.premium-dropdown .el-dropdown-menu__item:hover) {
  background-color: #f1f5f9 !important;
  color: #1e293b !important;
}

:deep(.premium-dropdown .el-dropdown-menu__item i) {
  margin-right: 8px !important;
  font-size: 16px !important;
  color: #94a3b8 !important;
}

.main-content {
  background-color: #f2f5f7;
  padding: 20px;
  height: calc(100vh - 60px);
  overflow-y: auto;
}

/* 主内容滚动条美化 */
.main-content::-webkit-scrollbar {
  width: 8px;
}

.main-content::-webkit-scrollbar-track {
  background: #f1f1f1;
}

.main-content::-webkit-scrollbar-thumb {
  background: #ccc;
  border-radius: 4px;
}

.main-content::-webkit-scrollbar-thumb:hover {
  background: #999;
}
</style>

<style>
/* 全局样式：将退出登录确认框移至顶部 */
.logout-dialog.el-message-box {
  vertical-align: top !important;
  margin-top: 15vh !important;
}
</style>
