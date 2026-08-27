import { describe, expect, it } from 'vitest';
import {
  MAX_ATTACHMENTS,
  MAX_ATTACHMENT_BYTES,
  validateAttachmentFile,
  validateAttachmentList,
} from './attachments';

function makeFile(name: string, size: number): File {
  return new File(['x'.repeat(size)], name);
}

describe('validateAttachmentFile', () => {
  it('接受文本类扩展名', () => {
    for (const name of ['app.log', 'config.yaml', 'data.json', 'notes.txt', 'kubelet']) {
      const res = validateAttachmentFile(makeFile(name, 10));
      expect(res.ok).toBe(true);
    }
  });

  it('拒绝超限大小', () => {
    const res = validateAttachmentFile(makeFile('big.log', MAX_ATTACHMENT_BYTES + 1));
    expect(res.ok).toBe(false);
    expect(res.error).toContain('超过大小上限');
  });

  it('拒绝不允许的扩展名', () => {
    const res = validateAttachmentFile(makeFile('dump.bin', 10));
    expect(res.ok).toBe(false);
    expect(res.error).toContain('.bin');
  });

  it('拒绝尾点文件名（与后端 filepath.Ext 一致）', () => {
    // `foo.` 后端扩展名为 "."（不在白名单），前端须同样拒绝，避免"前端放行→后端 400"
    const res = validateAttachmentFile(makeFile('foo.', 10));
    expect(res.ok).toBe(false);
    expect(res.error).toContain('.');
  });
});

describe('validateAttachmentList', () => {
  it('数量超限时报错', () => {
    const existing = Array.from({ length: MAX_ATTACHMENTS }, (_, i) => ({ name: `a${i}.log`, content: 'x' }));
    const res = validateAttachmentList(existing, { name: 'next.log', content: 'x' });
    expect(res.ok).toBe(false);
    expect(res.error).toContain('数量超过上限');
  });

  it('总量超限时报错', () => {
    // 单个均低于 400KB 上限、数量低于上限，仅总量越界
    const part = 'y'.repeat(350 * 1024);
    const existing = [{ name: 'a.log', content: part }, { name: 'b.log', content: part }];
    const res = validateAttachmentList(existing, { name: 'c.log', content: part });
    expect(res.ok).toBe(false);
    expect(res.error).toContain('总大小');
  });

  it('正常追加通过', () => {
    const res = validateAttachmentList([{ name: 'a.log', content: 'small' }], { name: 'b.log', content: 'tiny' });
    expect(res.ok).toBe(true);
  });
});
