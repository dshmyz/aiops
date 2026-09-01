import { onMounted, ref } from 'vue';
import { ElMessage } from 'element-plus';
import type { NotificationChannel } from '../types';
import { deleteNotificationChannel, listNotificationChannels, saveNotificationChannel } from '../api';

/** 新建用的空表单。 */
export function blankNotificationChannel(): NotificationChannel {
  return {
    id: '',
    type: 'webhook',
    name: '',
    url: '',
    secret: '',
    enabled: true,
  };
}

/** 通知通道的状态 + 数据流（列表/增删改）。变更即时热更新，无需重启服务。 */
export function useNotificationChannels() {
  const channels = ref<NotificationChannel[]>([]);
  const loading = ref(false);
  const saving = ref(false);
  const error = ref('');
  const configured = ref(true);

  // 编辑态：null = 未在编辑；'new' = 新建；NotificationChannel = 编辑既有通道。
  const editing = ref<null | 'new' | NotificationChannel>(null);
  const editForm = ref<NotificationChannel>(blankNotificationChannel());

  async function load() {
    loading.value = true;
    error.value = '';
    try {
      const body = await listNotificationChannels();
      if ('configured' in body && body.configured === false) {
        configured.value = false;
        channels.value = [];
      } else {
        configured.value = true;
        channels.value = body.channels ?? [];
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : '加载失败';
    } finally {
      loading.value = false;
    }
  }

  function startCreate() {
    editForm.value = blankNotificationChannel();
    editing.value = 'new';
  }

  function startEdit(channel: NotificationChannel) {
    editForm.value = { ...channel }; // secret 只写不回，编辑时留空
    editing.value = channel;
  }

  function cancelEdit() {
    editing.value = null;
  }

  async function save() {
    const form = editForm.value;
    if (!form.name.trim()) {
      ElMessage.error('通道名称不能为空');
      return;
    }
    if (!form.url.trim()) {
      ElMessage.error('Webhook 地址不能为空');
      return;
    }
    saving.value = true;
    try {
      await saveNotificationChannel(form);
      ElMessage.success('已保存，通知链已即时更新');
      editing.value = null;
      await load();
    } catch (e) {
      ElMessage.error(e instanceof Error ? e.message : '保存失败');
    } finally {
      saving.value = false;
    }
  }

  async function remove(channel: NotificationChannel) {
    try {
      await deleteNotificationChannel(channel.id);
      ElMessage.success(`已删除 "${channel.name}"`);
      await load();
    } catch (e) {
      ElMessage.error(e instanceof Error ? e.message : '删除失败');
    }
  }

  onMounted(load);

  return {
    channels,
    loading,
    saving,
    error,
    configured,
    editing,
    editForm,
    load,
    startCreate,
    startEdit,
    cancelEdit,
    save,
    remove,
  };
}
