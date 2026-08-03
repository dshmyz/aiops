<script setup lang="ts">
import { ElAlert } from 'element-plus';
import McpServerForm from '../components/McpServerForm.vue';
import McpServerList from '../components/McpServerList.vue';
import type { UseMCPServers } from '../composables/useMCPServers';

defineProps<{ mcpServers: UseMCPServers }>();
</script>

<template>
  <section data-test="mcp-servers-entry" data-view="mcp-servers" class="mcp-servers-entry">
    <header class="topbar">
      <div>
        <p class="eyebrow">MCP Hot Configuration</p>
        <h1>MCP 服务器管理</h1>
        <p class="topbar-copy">
          管理外部 MCP 服务器配置。新增、编辑或禁用后点击「热加载」即可增量注册/注销工具，无需重启服务。
        </p>
      </div>
      <div class="actions">
        <button
          class="mini-button"
          :disabled="mcpServers.mcpServersLoading.value"
          @click="mcpServers.refresh"
        >
          {{ mcpServers.mcpServersLoading.value ? '刷新中' : '刷新' }}
        </button>
        <button
          data-test="mcp-server-reload"
          class="mini-button"
          :disabled="mcpServers.mcpReloading.value"
          @click="mcpServers.reload"
        >
          {{ mcpServers.mcpReloading.value ? '热加载中' : '热加载' }}
        </button>
        <button data-test="mcp-server-new" class="primary-button" @click="mcpServers.openForm">
          + 新建 MCP 服务器
        </button>
      </div>
    </header>

    <el-alert v-if="mcpServers.error.value" class="alert" type="error" :title="mcpServers.error.value" show-icon />

    <McpServerList
      :servers="mcpServers.mcpServers.value"
      @toggle-enabled="mcpServers.toggleEnabled"
      @edit="mcpServers.editServer"
      @delete="mcpServers.remove"
    />

    <div v-if="mcpServers.mcpServerFormOpen.value" data-test="mcp-server-form-modal" class="form-modal">
      <McpServerForm
        :server="mcpServers.mcpServerEditing.value"
        @submit="mcpServers.save"
        @cancel="mcpServers.closeForm"
      />
    </div>
  </section>
</template>
