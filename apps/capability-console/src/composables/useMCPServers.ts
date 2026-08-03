import { onMounted, ref } from 'vue';
import type { Ref } from 'vue';
import {
  createMCPServer,
  deleteMCPServer,
  listMCPServers,
  reloadMCPServers,
  updateMCPServer,
} from '../api';
import type { MCPServer, SaveMCPServerPayload } from '../types';

/**
 * useMCPServers 管理 MCP 服务器热配置的前端状态：
 *  - 列表加载（refresh）
 *  - 新建/编辑表单开关（openForm / editServer / closeForm）
 *  - CRUD 委托 API（save / remove）
 *  - 启用切换（toggleEnabled）：按 id 查找完整 server，仅翻转 enabled 后整体 PUT
 *  - 热加载（reload）：触发后端增量注册/注销工具，成功后刷新列表
 *
 * 错误统一写入 error ref，由视图层展示；列表加载失败时静默保留旧状态，
 * 与 useScheduledTasks 行为一致。
 */
export interface UseMCPServers {
  mcpServers: Ref<MCPServer[]>;
  mcpServerFormOpen: Ref<boolean>;
  mcpServerEditing: Ref<MCPServer | null>;
  mcpServersLoading: Ref<boolean>;
  mcpReloading: Ref<boolean>;
  error: Ref<string>;
  refresh: () => Promise<void>;
  openForm: () => void;
  editServer: (server: MCPServer) => void;
  closeForm: () => void;
  save: (payload: SaveMCPServerPayload) => Promise<void>;
  remove: (id: string) => Promise<void>;
  toggleEnabled: (id: string, enabled: boolean) => Promise<void>;
  reload: () => Promise<void>;
}

export function useMCPServers(): UseMCPServers {
  const mcpServers = ref<MCPServer[]>([]);
  const mcpServerFormOpen = ref(false);
  const mcpServerEditing = ref<MCPServer | null>(null);
  const mcpServersLoading = ref(false);
  const mcpReloading = ref(false);
  const error = ref('');

  async function refresh() {
    mcpServersLoading.value = true;
    try {
      mcpServers.value = await listMCPServers();
    } catch {
      // 静默失败：列表页保留之前的状态，用户可手动重试
    } finally {
      mcpServersLoading.value = false;
    }
  }

  function openForm() {
    mcpServerEditing.value = null;
    mcpServerFormOpen.value = true;
  }

  function editServer(server: MCPServer) {
    mcpServerEditing.value = server;
    mcpServerFormOpen.value = true;
  }

  function closeForm() {
    mcpServerFormOpen.value = false;
    mcpServerEditing.value = null;
  }

  async function save(payload: SaveMCPServerPayload) {
    try {
      if (mcpServerEditing.value) {
        await updateMCPServer(mcpServerEditing.value.id, payload);
      } else {
        await createMCPServer(payload);
      }
      closeForm();
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '保存 MCP 服务器失败';
    }
  }

  async function remove(id: string) {
    try {
      await deleteMCPServer(id);
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '删除 MCP 服务器失败';
    }
  }

  async function toggleEnabled(id: string, enabled: boolean) {
    // 后端 PUT 要求完整 body，按 id 查找当前 server 构造 payload，仅翻转 enabled。
    const server = mcpServers.value.find((item: MCPServer) => item.id === id);
    if (!server) return;
    try {
      await updateMCPServer(id, {
        name: server.name,
        command: server.command,
        args: server.args,
        env: server.env,
        url: server.url,
        enabled,
      });
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '切换 MCP 服务器状态失败';
    }
  }

  async function reload() {
    mcpReloading.value = true;
    try {
      await reloadMCPServers();
      await refresh();
    } catch (err) {
      error.value = err instanceof Error ? err.message : '热加载 MCP 服务器失败';
    } finally {
      mcpReloading.value = false;
    }
  }

  onMounted(() => {
    void refresh();
  });

  return {
    mcpServers,
    mcpServerFormOpen,
    mcpServerEditing,
    mcpServersLoading,
    mcpReloading,
    error,
    refresh,
    openForm,
    editServer,
    closeForm,
    save,
    remove,
    toggleEnabled,
    reload,
  };
}
