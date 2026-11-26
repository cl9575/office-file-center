import dayjs from 'dayjs'

export const actionLabels: Record<string, string> = {
  login_success: '登录成功',
  login_failed: '登录失败',
  logout: '退出登录',
  password_change: '修改密码',
  setup_admin: '创建初始管理员',
  user_create: '创建账号',
  user_update: '修改账号',
  user_reset_password: '重置密码',
  user_delete: '删除账号',
  folder_create: '新建目录',
  upload_new: '上传文件',
  upload_version: '上传新版本',
  upload_failed: '上传失败',
  rename: '重命名',
  move: '移动',
  visibility_update: '修改可见范围',
  delete: '移入回收站',
  recycle_restore: '恢复回收站',
  recycle_purge: '永久删除',
  recycle_purge_all: '清空回收站',
  version_restore: '恢复历史版本',
  version_purge: '清理历史版本',
  preview: '预览',
  download: '下载',
  download_folder: '下载文件夹',
  settings_update: '修改设置',
  backup_success: '备份成功',
  backup_failed: '备份失败',
  backup_manual: '手动备份',
}

export const roleLabels: Record<string, string> = {
  admin: '管理员',
  operator: '操作员',
  editor: '编辑者',
  viewer: '只读者',
}

export const roleColors: Record<string, string> = {
  admin: '#7d6f5a',
  operator: '#5f7f9f',
  editor: '#3a79bc',
  viewer: 'default',
}

export const resultLabels: Record<string, string> = {
  success: '成功',
  failure: '失败',
  partial: '部分完成',
}

export const resultColors: Record<string, string> = {
  success: '#587d63',
  failure: '#a9635d',
  partial: '#96703f',
}

export function fmtTime(value: string): string {
  if (!value) return '—'
  const parsed = dayjs(value)
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm:ss') : value
}

export function actionLabel(action: string): string {
  return actionLabels[action] || action
}
