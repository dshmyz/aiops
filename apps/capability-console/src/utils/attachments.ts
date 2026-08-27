/**
 * 消息附件（日志/文本文件）的前端模型与读取工具。
 *
 * 与后端 assistant.Attachment 对齐（name + content 全文内联传输）；
 * 常量与服务端约束保持一致：数量 5、单文件 400KB、总 1MB。
 */

export interface MessageAttachment {
  name: string;
  content: string;
}

export const MAX_ATTACHMENTS = 5;
export const MAX_ATTACHMENT_BYTES = 400 * 1024;
export const MAX_TOTAL_ATTACHMENT_BYTES = 1024 * 1024;

/** 与后端白名单一致的文本类扩展名；空扩展名放行（容器日志常无后缀）。 */
const ALLOWED_EXTS = new Set([
  'log', 'txt', 'text', 'json', 'yaml', 'yml', 'xml', 'csv',
  'conf', 'ini', 'properties', 'out',
]);

export interface AttachmentValidationResult {
  ok: boolean;
  error?: string;
}

/** 单文件校验（加入列表前逐个检查，错误文案直接面向用户展示）。 */
export function validateAttachmentFile(file: File): AttachmentValidationResult {
  if (file.size > MAX_ATTACHMENT_BYTES) {
    return { ok: false, error: `「${file.name}」超过大小上限（最大 ${Math.round(MAX_ATTACHMENT_BYTES / 1024)} KB，实际 ${Math.round(file.size / 1024)} KB）` };
  }
  const dot = file.name.lastIndexOf('.');
  // 与后端 filepath.Ext 对齐：`foo.` 的后端扩展名是 "."（拒），`kubelet` 无点是 ""
  //（放行）。之前前端把 `foo.` 当空后缀放行，前端接收、后端 400，判法不一致。
  const ext = dot >= 0 && dot < file.name.length - 1
    ? file.name.slice(dot + 1).toLowerCase()
    : (dot >= 0 ? '.' : '');
  if (ext && !ALLOWED_EXTS.has(ext)) {
    return { ok: false, error: `暂不支持的文件类型 .${ext}（${file.name}）：请转存为 .txt 或直接粘贴文本` };
  }
  return { ok: true };
}

/** 整表校验（数量 + 总量），追加前调用。总量以 UTF-8 字节计，与后端 len(Content) 一致
 *（不能用 content.length —— 那是 UTF-16 码元数，中文日志会低估近 3 倍而绕过后端限额）。 */
export function validateAttachmentList(existing: MessageAttachment[], next: MessageAttachment): AttachmentValidationResult {
  if (existing.length + 1 > MAX_ATTACHMENTS) {
    return { ok: false, error: `附件数量超过上限（最多 ${MAX_ATTACHMENTS} 个）` };
  }
  const toBytes = (s: string) => new TextEncoder().encode(s).length;
  const total = existing.reduce((sum, a) => sum + toBytes(a.content), 0) + toBytes(next.content);
  if (total > MAX_TOTAL_ATTACHMENT_BYTES) {
    return { ok: false, error: '附件总大小超过上限（最大 1 MB）' };
  }
  return { ok: true };
}

/**
 * File → 文本附件。二进制拒绝由后端兜底，这里用 NUL 字节做一层前置探测，
 * 提前给出可读报错而不是让请求打到服务端才失败。
 */
export function readAttachmentFile(file: File): Promise<MessageAttachment> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const text = String(reader.result ?? '');
      // 前 8KB 出现 NUL 视为二进制（与后端规则一致）
      if (text.slice(0, 8192).includes('\u0000')) {
        reject(new Error(`「${file.name}」疑似二进制文件，仅支持文本内容`));
        return;
      }
      resolve({ name: file.name, content: text });
    };
    reader.onerror = () => reject(new Error(`无法读取文件「${file.name}」`));
    reader.readAsText(file);
  });
}
