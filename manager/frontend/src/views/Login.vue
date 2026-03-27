<template>
  <div class="login-container">
    <el-card class="login-card">
      <template #header>
        <div class="card-header">
          <h2>布法罗智能体管理系统</h2>
        </div>
      </template>

      <el-tabs v-model="activeTab" class="login-tabs">
        <el-tab-pane label="登录" name="login">
          <el-form
            ref="loginFormRef"
            :model="loginForm"
            :rules="loginRules"
            label-position="top"
            hide-required-asterisk
            class="premium-form"
          >
            <el-form-item label="用户名" prop="username">
              <el-input
                v-model="loginForm.username"
                placeholder="请输入用户名"
              />
            </el-form-item>
            <el-form-item label="密码" prop="password">
              <el-input
                v-model="loginForm.password"
                type="password"
                placeholder="请输入密码"
                @keyup.enter="handleLogin"
              />
            </el-form-item>
          </el-form>

          <el-button
            type="primary"
            :loading="loading"
            @click="handleLogin"
            style="width: 100%; margin-top: 20px; margin-bottom: 10px"
          >
            登录
          </el-button>
        </el-tab-pane>

        <el-tab-pane label="注册" name="register">
          <el-form
            ref="registerFormRef"
            :model="registerForm"
            :rules="registerRules"
            label-position="top"
            hide-required-asterisk
            class="premium-form"
          >
            <el-form-item label="用户名" prop="username">
              <el-input
                v-model="registerForm.username"
                placeholder="请输入用户名"
              />
            </el-form-item>
            <el-form-item label="邮箱" prop="email">
              <el-input v-model="registerForm.email" placeholder="请输入邮箱" />
            </el-form-item>
            <el-form-item label="密码" prop="password">
              <el-input
                v-model="registerForm.password"
                type="password"
                placeholder="请输入密码"
              />
            </el-form-item>
            <el-form-item label="确认密码" prop="confirmPassword">
              <el-input
                v-model="registerForm.confirmPassword"
                type="password"
                placeholder="请确认密码"
                @keyup.enter="handleRegister"
              />
            </el-form-item>
          </el-form>

          <el-button
            type="primary"
            :loading="loading"
            @click="handleRegister"
            style="width: 100%; margin-top: 20px; margin-bottom: 10px"
          >
            注册
          </el-button>
        </el-tab-pane>
      </el-tabs>
      <div class="public-links">
        <router-link to="/openapi-docs">查看公开 OpenAPI 接口说明</router-link>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from "vue";
import { useRouter } from "vue-router";
import { ElMessage } from "element-plus";
import { useAuthStore } from "../stores/auth";
import { getPostLoginRedirectPath } from "../utils/authRedirect";
import { checkNeedsSetup } from "../utils/setupStatus";

const router = useRouter();
const authStore = useAuthStore();

const activeTab = ref("login");
const loading = ref(false);
const loginFormRef = ref();
const registerFormRef = ref();

const loginForm = reactive({
  username: "",
  password: "",
});

const registerForm = reactive({
  username: "",
  email: "",
  password: "",
  confirmPassword: "",
});

const loginRules = {
  username: [{ required: true, message: "请输入用户名", trigger: "blur" }],
  password: [{ required: true, message: "请输入密码", trigger: "blur" }],
};

const registerRules = {
  username: [{ required: true, message: "请输入用户名", trigger: "blur" }],
  email: [
    { required: true, message: "请输入邮箱", trigger: "blur" },
    { type: "email", message: "请输入正确的邮箱格式", trigger: "blur" },
  ],
  password: [
    { required: true, message: "请输入密码", trigger: "blur" },
    { min: 6, message: "密码长度不能少于6位", trigger: "blur" },
  ],
  confirmPassword: [
    { required: true, message: "请确认密码", trigger: "blur" },
    {
      validator: (rule, value, callback) => {
        if (value !== registerForm.password) {
          callback(new Error("两次输入密码不一致"));
        } else {
          callback();
        }
      },
      trigger: "blur",
    },
  ],
};

const handleLogin = async () => {
  if (!loginFormRef.value) return;

  await loginFormRef.value.validate(async (valid) => {
    if (valid) {
      loading.value = true;
      const result = await authStore.login(loginForm);
      loading.value = false;

      if (result.success) {
        ElMessage.success("登录成功");
        router.push(getPostLoginRedirectPath(authStore.user));
      } else {
        ElMessage.error(result.message);
      }
    }
  });
};

const handleRegister = async () => {
  if (!registerFormRef.value) return;

  await registerFormRef.value.validate(async (valid) => {
    if (valid) {
      loading.value = true;
      const result = await authStore.register(registerForm);
      loading.value = false;

      if (result.success) {
        ElMessage.success("注册成功，请登录");
        activeTab.value = "login";
        Object.assign(registerForm, {
          username: "",
          email: "",
          password: "",
          confirmPassword: "",
        });
      } else {
        ElMessage.error(result.message);
      }
    }
  });
};

// 检查系统状态，如果未初始化则跳转到引导页面
const checkSystemStatus = async () => {
  try {
    if (await checkNeedsSetup()) {
      router.push("/setup");
    }
  } catch (error) {
    console.error("检查系统状态失败:", error);
  }
};

onMounted(() => {
  checkSystemStatus();
});
</script>

<style scoped>
.public-links {
  margin-top: 8px;
  text-align: center;
  font-size: 13px;
}
.public-links a {
  color: #409eff;
  text-decoration: none;
}
.public-links a:hover {
  text-decoration: underline;
}

.login-container {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 100vh;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
}

.login-card {
  width: 400px;
  border-radius: 12px;
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.1);
  margin-bottom: 50px;
}

.card-header {
  text-align: center;
  padding: 10px 0 20px;
}

.card-header h2 {
  margin: 0;
  font-size: 22px;
  color: #333;
}

.login-tabs {
  margin-top: 0px;
}

:deep(.el-tabs__header) {
  margin-bottom: 25px;
}

:deep(.el-tabs__nav-container) {
  margin-bottom: 20px;
}

:deep(.el-tabs__nav-scroll) {
  display: flex;
  justify-content: center;
}

:deep(.el-tabs__item) {
  font-size: 16px;
  font-weight: 500;
  height: 44px;
  padding: 0 40px;
  transition: all 0.3s;
}

:deep(.el-tabs__item.is-active) {
  font-weight: 700;
  color: #409eff;
}

:deep(.el-tabs__active-bar) {
  height: 3px;
  border-radius: 3px;
  background-color: #409eff;
}

:deep(.el-tabs__nav-wrap::after) {
  height: 1px;
  background-color: #f1f5f9;
}

/* 输入框与按钮圆角优化 */
:deep(.el-input__wrapper) {
  border-radius: 8px;
  box-shadow: 0 0 0 1px #dcdfe6 inset;
  transition: all 0.2s;
}

:deep(.el-input__wrapper:hover) {
  box-shadow: 0 0 0 1px #409eff inset;
}

.login-card .el-button--primary {
  width: 100%;
  height: 42px;
  margin-top: 15px;
  margin-bottom: 5px;
  border-radius: 8px;
  font-weight: 600;
  font-size: 15px;
  background: linear-gradient(135deg, #409eff 0%, #3a8ee6 100%);
  border: none;
  box-shadow: 0 4px 12px rgba(64, 158, 255, 0.2);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
}

.login-card .el-button--primary:hover {
  transform: translateY(-1px);
  box-shadow: 0 6px 16px rgba(64, 158, 255, 0.3);
  opacity: 0.9;
}

.login-card .el-button--primary:active {
  transform: translateY(0);
}

:deep(.el-card__header) {
  border-bottom: none;
  padding-bottom: 0;
}

:deep(.el-card__body) {
  padding: 10px 30px 25px;
}
</style>
