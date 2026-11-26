import { useEffect, useMemo, useState } from 'react'
import { App, Checkbox, Modal, Radio, Select, Space, Spin, Typography } from 'antd'
import { api } from '../api'
import type { Role, VisibilityChoice, VisibilityInfo, VisibilityOptions } from '../types'
import { roleLabels } from '../labels'

const { Text } = Typography

function initialChoice(initial?: VisibilityInfo | VisibilityChoice): VisibilityChoice {
  return {
    mode: initial?.mode || 'default',
    roles: initial?.roles ? [...initial.roles] : [],
    users: initial?.users
      ? initial.users.map((user) => (typeof user === 'string' ? user : user.id))
      : [],
  }
}

export function VisibilityModal({
  open,
  initial,
  confirmLoading,
  onCancel,
  onOk,
}: {
  open: boolean
  initial?: VisibilityInfo | VisibilityChoice
  confirmLoading?: boolean
  onCancel: () => void
  onOk: (choice: VisibilityChoice) => void
}) {
  const { message } = App.useApp()
  const [options, setOptions] = useState<VisibilityOptions | null>(null)
  const [loading, setLoading] = useState(false)
  const [choice, setChoice] = useState<VisibilityChoice>(() => initialChoice(initial))

  useEffect(() => {
    if (!open) return
    setChoice(initialChoice(initial))
    setLoading(true)
    api
      .visibilityOptions()
      .then(setOptions)
      .catch((err) => message.error(err instanceof Error ? err.message : '无法读取可见范围选项'))
      .finally(() => setLoading(false))
    // 仅在打开时初始化；编辑过程中不回写弹窗状态。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, message])

  const userOptions = useMemo(
    () =>
      (options?.users || []).map((user) => ({
        value: user.id,
        label: `${user.display_name}（${roleLabels[user.role] || user.role}）`,
      })),
    [options],
  )

  function submit() {
    if (choice.mode === 'custom' && choice.roles.length === 0 && choice.users.length === 0) {
      message.error('请至少选择一个权限组或用户')
      return
    }
    onOk(choice)
  }

  return (
    <Modal
      title="设置可见范围"
      open={open}
      okText="确定"
      cancelText="取消"
      confirmLoading={confirmLoading}
      onCancel={onCancel}
      onOk={submit}
      destroyOnClose={false}
    >
      <Spin spinning={loading}>
        <Space direction="vertical" size={18} style={{ width: '100%' }}>
          <Radio.Group
            value={choice.mode}
            onChange={(event) => setChoice((current) => ({ ...current, mode: event.target.value }))}
          >
            <Space direction="vertical" size={10}>
              <Radio value="default">
                默认规则
                <Text type="secondary" style={{ marginLeft: 8 }}>更高权限和本人可见</Text>
              </Radio>
              <Radio value="custom">
                指定可见
                <Text type="secondary" style={{ marginLeft: 8 }}>仅选择的对象可见</Text>
              </Radio>
            </Space>
          </Radio.Group>

          {choice.mode === 'custom' && (
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
              <div>
                <div style={{ marginBottom: 8 }}>权限组</div>
                <Checkbox.Group
                  value={choice.roles}
                  options={(options?.roles || []).map((role: Role) => ({
                    value: role,
                    label: roleLabels[role] || role,
                  }))}
                  onChange={(values) =>
                    setChoice((current) => ({ ...current, roles: values as Role[] }))
                  }
                />
              </div>
              <div>
                <div style={{ marginBottom: 8 }}>指定用户</div>
                <Select
                  mode="multiple"
                  allowClear
                  showSearch
                  optionFilterProp="label"
                  style={{ width: '100%' }}
                  placeholder="选择一个或多个用户"
                  value={choice.users}
                  options={userOptions}
                  onChange={(values) => setChoice((current) => ({ ...current, users: values }))}
                />
              </div>
            </Space>
          )}
        </Space>
      </Spin>
    </Modal>
  )
}
