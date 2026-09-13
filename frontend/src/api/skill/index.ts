import { get, postUpload } from "../../utils/request";

// Skill信息
export interface SkillInfo {
  name: string;
  description: string;
}

// 获取预装Skills列表；skills_available 为 false 表示沙箱未启用，前端应隐藏/禁用 Skills 配置
export function listSkills() {
  return get<{ data: SkillInfo[]; skills_available?: boolean }>('/api/v1/skills');
}

// 上传并安装技能 ZIP（服务端校验 SKILL.md → 解压到技能目录 → 同步调用 agent-gateway /skills/install）
export function uploadSkill(file: File, onUploadProgress?: (progressEvent: any) => void) {
  const formData = new FormData();
  formData.append('file', file);
  return postUpload('/api/v1/skills/upload', formData, onUploadProgress);
}
