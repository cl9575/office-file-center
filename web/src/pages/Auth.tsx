import { useState } from 'react'
import { Alert, App, Button, Form, Input, Modal, Result, Typography } from 'antd'
import { CloudServerOutlined, LockOutlined, SafetyOutlined, UserOutlined } from '@ant-design/icons'
import { api, ApiError } from '../api'
import type { PublicSettings, SetupStatus, User } from '../types'

const { Title, Text, Paragraph } = Typography

function AuthLayout({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <div className="auth-page">
      <div className="auth-card">
        <div className="auth-brand">
          <div className="auth-logo">
            <CloudServerOutlined />
          </div>
          <div>
            <div className="auth-brand-name">办公室文件中心</div>
            <div className="auth-brand-sub">安全、清晰的内部文件版本管理</div>
          </div>
          <div className="auth-points">
            <div><SafetyOutlined /> 文件历史可追溯、可恢复</div>
            <div><LockOutlined /> 局域网内访问，账号独立</div>
          </div>
          <div className="auth-foot">数据保存在办公室主机，不经过外部网络</div>
        </div>
        <div className="auth-form">
          <Title level={3}>{title}</Title>
          <Paragraph type="secondary">{subtitle}</Paragraph>
          {children}
        </div>
      </div>
    </div>
  )
}

export function LoginPage({
  siteName,
  onSuccess,
}: {
  siteName: string
  onSuccess: (user: User, settings: PublicSettings) => void
}) {
  const { message } = App.useApp()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  return (
    <AuthLayout title={siteName || '办公室文件中心'} subtitle="使用管理员分配的账号登录">
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 20 }} />}
      <Form
        layout="vertical"
        requiredMark={false}
        onFinish={async (values) => {
          setLoading(true)
          setError('')
          try {
            const state = await api.login(values.username.trim(), values.password)
            message.success(`欢迎回来，${state.user.display_name}`)
            onSuccess(state.user, state.settings)
          } catch (err) {
            setError(err instanceof ApiError ? err.message : '登录失败，请稍后重试')
          } finally {
            setLoading(false)
          }
        }}
      >
        <Form.Item name="username" label="用户名" rules={[{ required: true, message: '请输入用户名' }]}>
          <Input size="large" prefix={<UserOutlined />} placeholder="请输入用户名" autoFocus autoComplete="username" />
        </Form.Item>
        <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
          <Input.Password size="large" prefix={<LockOutlined />} placeholder="请输入密码" autoComplete="current-password" />
        </Form.Item>
        <Button type="primary" size="large" htmlType="submit" block loading={loading}>
          登录
        </Button>
      </Form>
      <Text type="secondary" className="auth-note">账号由管理员创建。忘记密码请联系管理员重置。</Text>
    </AuthLayout>
  )
}

export function SetupPage({
  status,
  onSuccess,
}: {
  status: SetupStatus
  onSuccess: (user: User, settings: PublicSettings) => void
}) {
  const { message } = App.useApp()
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  if (!status.local) {
    return (
      <AuthLayout title="系统尚未初始化" subtitle="为了安全，初始管理员只能在这台主机上创建">
        <Result
          status="warning"
          title="请在服务器主机上完成初始化"
          subTitle={`在主机上打开访问地址，或暂时通过 ${window.location.origin} 完成设置。初始化后即可从其他电脑登录。`}
        />
      </AuthLayout>
    )
  }

  return (
    <AuthLayout title="创建初始管理员" subtitle="这是首次启动。请设置管理员账号，之后由管理员为同事创建账号。">
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 20 }} />}
      <Form
        layout="vertical"
        requiredMark={false}
        onFinish={async (values) => {
          setLoading(true)
          setError('')
          try {
            const state = await api.setupAdmin({
              username: values.username.trim(),
              display_name: values.display_name.trim(),
              password: values.password,
            })
            message.success('初始化完成')
            onSuccess(state.user, state.settings)
          } catch (err) {
            setError(err instanceof ApiError ? err.message : '初始化失败，请稍后重试')
          } finally {
            setLoading(false)
          }
        }}
      >
        <Form.Item
          name="username"
          label="管理员用户名"
          rules={[
            { required: true, message: '请输入用户名' },
            { pattern: /^[A-Za-z0-9_.-]{3,32}$/, message: '3 到 32 位字母、数字、下划线、点或短横线' },
          ]}
        >
          <Input size="large" prefix={<UserOutlined />} placeholder="例如 admin" autoFocus />
        </Form.Item>
        <Form.Item name="display_name" label="显示名称" rules={[{ required: true, message: '请输入显示名称' }]}>
          <Input size="large" placeholder="例如 张主管" maxLength={40} />
        </Form.Item>
        <Form.Item
          name="password"
          label="密码"
          rules={[
            { required: true, message: '请输入密码' },
            { min: 6, message: '密码至少需要 6 个字符' },
          ]}
        >
          <Input.Password size="large" prefix={<LockOutlined />} placeholder="至少 6 个字符" autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          name="confirm"
          label="确认密码"
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
          <Input.Password size="large" prefix={<LockOutlined />} placeholder="再次输入密码" autoComplete="new-password" />
        </Form.Item>
        <Button type="primary" size="large" htmlType="submit" block loading={loading}>
          完成初始化
        </Button>
      </Form>
    </AuthLayout>
  )
}

export function ChangePasswordModal({
  open,
  forced,
  onClose,
  onSuccess,
}: {
  open: boolean
  forced: boolean
  onClose: () => void
  onSuccess: () => void
}) {
  const { message } = App.useApp()
  const [form] = Form.useForm()
  const [loading, setLoading] = useState(false)

  return (
    <Modal
      open={open}
      title={forced ? '密码已重置：请设置新密码' : '修改密码'}
      okText="确认修改"
      cancelText="取消"
      confirmLoading={loading}
      closable={!forced}
      maskClosable={!forced}
      keyboard={!forced}
      cancelButtonProps={{ style: forced ? { display: 'none' } : undefined }}
      onCancel={onClose}
      afterClose={() => form.resetFields()}
      onOk={async () => {
        try {
          const values = await form.validateFields()
          setLoading(true)
          await api.changePassword(values.old_password, values.new_password)
          message.success('密码已更新')
          form.resetFields()
          onSuccess()
        } catch (err) {
          if (err instanceof ApiError) {
            message.error(err.message)
          }
        } finally {
          setLoading(false)
        }
      }}
    >
      {forced && (
        <Alert
          type="info"
          showIcon
          message="管理员重置了你的密码。为了账号安全，请先设置新密码，修改成功后才能继续使用。"
          style={{ marginBottom: 20 }}
        />
      )}
      <Form form={form} layout="vertical" requiredMark={false}>
        <Form.Item name="old_password" label="当前密码" rules={[{ required: true, message: '请输入当前密码' }]}>
          <Input.Password prefix={<LockOutlined />} autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          name="new_password"
          label="新密码"
          rules={[
            { required: true, message: '请输入新密码' },
            { min: 6, message: '密码至少需要 6 个字符' },
          ]}
        >
          <Input.Password prefix={<LockOutlined />} autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          name="confirm"
          label="确认新密码"
          dependencies={['new_password']}
          rules={[
            { required: true, message: '请再次输入新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('new_password') === value) return Promise.resolve()
                return Promise.reject(new Error('两次输入的密码不一致'))
              },
            }),
          ]}
        >
          <Input.Password prefix={<LockOutlined />} autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Modal>
  )
}
