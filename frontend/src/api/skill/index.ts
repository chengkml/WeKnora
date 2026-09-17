import { get, getDown, postUpload, del } from "../../utils/request";
import type { AxiosProgressEvent } from "axios";

// Skill信息
export interface SkillInfo {
  name: string;
  description: string;
}

// SkillDetail 技能详情（文件清单）
export interface SkillDetail {
  name: string;
  description: string;
  path: string;
  file_count: number;
  files: { path: string; size: number }[];
  total_files: number;
}

// 获取预装Skills列表；skills_available 为 false 表示沙箱未启用，前端应隐藏/禁用 Skills 配置
export function listSkills() {
  return get<{ data: SkillInfo[]; skills_available?: boolean }>('/api/v1/skills');
}

// 上传并安装技能 ZIP（服务端校验 SKILL.md → 解压到技能目录 → 同步调用 agent-gateway /skills/install）
export function uploadSkill(file: File, onUploadProgress?: (progressEvent: AxiosProgressEvent) => void) {
  const formData = new FormData();
  formData.append('file', file);
  return postUpload('/api/v1/skills/upload', formData, onUploadProgress);
}

// 查看技能详情（文件清单）
export function getSkillDetail(name: string) {
  return get<{ data: SkillDetail }>(`/api/v1/skills/${encodeURIComponent(name)}`);
}

// 导出技能 ZIP（与上传安装格式一致，可直接重新导入）
export function exportSkill(name: string) {
  return getDown(`/api/v1/skills/${encodeURIComponent(name)}/export`);
}

// 删除技能（删除 WeKnora 侧 + 同步 agent-gateway）
export function deleteSkill(name: string) {
  return del<{ data: { name: string } }>(`/api/v1/skills/${encodeURIComponent(name)}`);
}
