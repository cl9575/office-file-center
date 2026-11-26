import { useEffect, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
} from 'antd'
import { EditOutlined, KeyOutlined, PlusOutlined, UserDeleteOutlined, UserOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { api, ApiError } from '../api'
import type { Role, User } from '../types'
import { fmtTime, roleColors, roleLabels } from '../labels'

interface CreateForm {
  username: string
  display_name: string
  password: string
  confirm: string
  role: Role
}

interface EditForm {
  display_name: string
  role: Role
  disabled: boolean
}

interface ResetForm {
  password: string
  confirm: string
}

export function AdminUsers({ currentUser }: { currentUser: User }) {
  const { message, modal } = App.useApp()
  const canManageUsers = currentUser.role === 'admin'
  const assignableRoles: Role[] = currentUser.is_owner
    ? ['admin', 'operator', 'editor', 'viewer']
    : currentUser.role === 'admin'
      ? ['operator', 'editor', 'viewer']
      : []
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(true)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<User | null>(null)
  const [resetting, setResetting] = useState<User | null>(null)
  const [saving, setSaving] = useState(false)
  const [createForm] = Form.useForm<CreateForm>()
  const [editForm] = Form.useForm<EditForm>()
  const [resetForm] = Form.useForm<ResetForm>()
  const roleOptions = (roles: Role[]) => roles.map((role) => ({ value: role, label: roleLabels[role] }))

  async function load() {
    setLoading(true)
    try {
      setUsers(await api.users())
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  useEffect(() => {
    if (editing) {
      editForm.setFieldsValue({
        display_name: editing.display_name,
        role: editing.role,
        disabled: editing.disabled,
      })
    }
  }, [editing, editForm])

  async function createUser() {
    try {
      const values = await createForm.validateFields()
      setSaving(true)
      await api.createUser({
        username: values.username.trim(),
        display_name: values.display_name.trim(),
        password: values.password,
        role: values.role,
      })
      message.success('账号已创建，请将初始密码告知对方；首次登录会提示修改密码')
      setCreateOpen(false)
      createForm.resetFields()
      await load()
    } catch (err) {
      if (err instanceof ApiError) message.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function updateUser() {
    if (!editing) return
    try {
      const values = await editForm.validateFields()
      setSaving(true)
      await api.updateUser(editing.id, {
        display_name: values.display_name.trim(),
        role: values.role,
        disabled: values.disabled,
      })
      message.success('账号已更新')
      setEditing(null)
      await load()
    } catch (err) {
      if (err instanceof ApiError) message.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function resetPassword() {
    if (!resetting) return
    try {
      const values = await resetForm.validateFields()
      setSaving(true)
      await api.resetPassword(resetting.id, values.password)
      message.success('密码已重置，该账号需要重新登录并修改密码')
      setResetting(null)
      resetForm.resetFields()
    } catch (err) {
      if (err instanceof ApiError) message.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  function deleteUser(row: User) {
    modal.confirm({
      title: `删除账号「${row.display_name}」？`,
      content:
        '删除后该账号将无法再登录，但已上传的文件、历史版本和操作记录会保留。若只是暂时停用，请使用“编辑 → 停用账号”。此操作不可撤销。',
      okText: '删除账号',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await api.deleteUser(row.id)
          message.success('账号已删除')
          await load()
        } catch (err) {
          if (err instanceof ApiError) message.error(err.message)
        }
      },
    })
  }

  const editRoleOptions = roleOptions(
    editing?.id === currentUser.id
      ? (['admin', 'operator', 'editor', 'viewer'] as Role[])
      : assignableRoles,
  )

  const columns: ColumnsType<User> = [
    {
      title: '账号',
      key: 'username',
      render: (_, row) => (
        <Space>
          <span className="user-avatar-small"><UserOutlined /></span>
          <Space direction="vertical" size={0}>
            <span style={{ fontWeight: 600 }}>{row.display_name}</span>
            <Space size={6}>
              <span className="muted">@{row.username}</span>
              {row.is_owner && <Tag color="#3a79bc">主机账号</Tag>}
            </Space>
          </Space>
        </Space>
      ),
    },
    {
      title: '角色',
      dataIndex: 'role',
      width: 110,
      render: (value: string) => <Tag color={roleColors[value]}>{roleLabels[value]}</Tag>,
    },
    {
      title: '状态',
      key: 'status',
      width: 130,
      render: (_, row) => (
        <Space size={4} wrap>
          {row.disabled ? <Tag>已停用</Tag> : <Tag color="#587d63">正常</Tag>}
          {row.must_change_password && <Tag color="#96703f">待改密</Tag>}
        </Space>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 175,
      render: (value: string) => <span className="muted">{fmtTime(value)}</span>,
    },
    {
      title: '操作',
      key: 'actions',
      width: 250,
      render: (_, row) => {
        const isSelf = row.id === currentUser.id
        if (!canManageUsers) {
          return <span className="muted">操作员仅可查看</span>
        }
        if (row.is_owner && !isSelf) {
          return <span className="muted">仅主机账号本人可维护</span>
        }
        if (!isSelf && !currentUser.is_owner && row.role === currentUser.role) {
          return <span className="muted">同级账号仅主机账号可维护</span>
        }
        return (
          <Space size={0}>
            <Button type="link" size="small" icon={<EditOutlined />} onClick={() => setEditing(row)}>
              编辑
            </Button>
            <Button type="link" size="small" icon={<KeyOutlined />} onClick={() => setResetting(row)}>
              重置密码
            </Button>
            {currentUser.is_owner && !row.is_owner && !isSelf && (
              <Button
                type="link"
                size="small"
                danger
                icon={<UserDeleteOutlined />}
                onClick={() => deleteUser(row)}
              >
                删除
              </Button>
            )}
          </Space>
        )
      },
    },
  ]

  return (
    <div className="admin-page">
      <div className="page-heading">
        <div>
          <h1>用户管理</h1>
          <p>管理员维护账号；操作员可进入后台查看账号；编辑者维护文件；只读者浏览和下载。</p>
        </div>
        {canManageUsers && (
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
            新建账号
          </Button>
        )}
      </div>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 18 }}
        message={
          !canManageUsers
            ? '操作员仅可查看账号。'
            : currentUser.is_owner
              ? '只有主机账号可以设置管理员。'
              : '管理员最高可设置操作员。'
        }
      />
      <Table className="admin-users-table" rowKey="id" columns={columns} dataSource={users} loading={loading} pagination={false} />

      <Modal
        title="新建账号"
        open={createOpen}
        okText="创建"
        cancelText="取消"
        confirmLoading={saving}
        onCancel={() => setCreateOpen(false)}
        onOk={() => void createUser()}
        forceRender
      >
        <Form form={createForm} layout="vertical" initialValues={{ role: 'editor' }} requiredMark={false}>
          <Form.Item
            name="username"
            label="用户名"
            rules={[
              { required: true, message: '请输入用户名' },
              { pattern: /^[A-Za-z0-9_.-]{3,32}$/, message: '3 到 32 位字母、数字、下划线、点或短横线' },
            ]}
          >
            <Input placeholder="例如 zhangsan" autoComplete="off" />
          </Form.Item>
          <Form.Item name="display_name" label="显示名称" rules={[{ required: true, message: '请输入显示名称' }]}>
            <Input placeholder="例如 张三" maxLength={40} />
          </Form.Item>
          <Form.Item name="role" label="角色" rules={[{ required: true }]}>
            <Select options={roleOptions(assignableRoles)} />
          </Form.Item>
          <Form.Item
            name="password"
            label="初始密码"
            rules={[
              { required: true, message: '请输入初始密码' },
              { min: 6, message: '密码至少需要 6 个字符' },
            ]}
          >
            <Input.Password placeholder="至少 6 个字符" autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirm"
            label="确认初始密码"
            dependencies={['password']}
            rules={[
              { required: true, message: '请再次输入密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('password') === value) return Promise.resolve()
                  return Promise.reject(new Error('两次输入的密码不一致'))
                },
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={`编辑账号：${editing?.display_name || ''}`}
        open={!!editing}
        okText="保存"
        cancelText="取消"
        confirmLoading={saving}
        onCancel={() => setEditing(null)}
        onOk={() => void updateUser()}
        forceRender
      >
        <Form form={editForm} layout="vertical" requiredMark={false}>
          <Form.Item label="用户名">
            <Input value={editing?.username} disabled />
          </Form.Item>
          <Form.Item name="display_name" label="显示名称" rules={[{ required: true, message: '请输入显示名称' }]}>
            <Input maxLength={40} />
          </Form.Item>
          <Form.Item name="role" label="角色" rules={[{ required: true }]}>
            <Select
              disabled={!!editing?.is_owner || editing?.id === currentUser.id}
              options={editRoleOptions}
            />
          </Form.Item>
          <Form.Item name="disabled" label="停用账号" valuePropName="checked">
            <Switch
              disabled={editing?.id === currentUser.id || !!editing?.is_owner}
              checkedChildren="已停用"
              unCheckedChildren="正常"
            />
          </Form.Item>
          {editing?.is_owner && (
            <span className="muted">主机账号固定为管理员，不能降级或停用。</span>
          )}
          {editing?.id === currentUser.id && !editing?.is_owner && (
            <Space direction="vertical" size={2}>
              <span className="muted">不能修改自己的权限等级。</span>
              <Tooltip title="不能停用当前登录的账号">
                <span className="muted">当前登录账号不能被停用。</span>
              </Tooltip>
            </Space>
          )}
        </Form>
      </Modal>

      <Modal
        title={`重置密码：${resetting?.display_name || ''}`}
        open={!!resetting}
        okText="重置密码"
        cancelText="取消"
        confirmLoading={saving}
        onCancel={() => setResetting(null)}
        onOk={() => void resetPassword()}
        forceRender
      >
        <Alert
          type="warning"
          showIcon
          message="重置后该账号的现有登录会失效，下次登录时必须修改密码。"
          style={{ marginBottom: 16 }}
        />
        <Form form={resetForm} layout="vertical" requiredMark={false}>
          <Form.Item
            name="password"
            label="新密码"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 6, message: '密码至少需要 6 个字符' },
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirm"
            label="确认新密码"
            dependencies={['password']}
            rules={[
              { required: true, message: '请再次输入密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('password') === value) return Promise.resolve()
                  return Promise.reject(new Error('两次输入的密码不一致'))
                },
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
