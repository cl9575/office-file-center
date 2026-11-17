import { useEffect, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Row,
  Space,
  Spin,
  Switch,
  TimePicker,
  Typography,
} from 'antd'
import { SaveOutlined } from '@ant-design/icons'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { api } from '../api'
import type { AdminSettings as AdminSettingsType } from '../types'

const { Text, Paragraph } = Typography
const GIB = 1024 * 1024 * 1024

interface SettingsForm {
  site_name: string
  max_file_size_gb: number
  free_space_reserve_gb: number
  recycle_retention_days: number
  backup_enabled: boolean
  backup_time: Dayjs
  backup_keep: number
  backup_dir: string
}

export function AdminSettings({ onSaved }: { onSaved: () => void }) {
  const { message } = App.useApp()
  const [form] = Form.useForm<SettingsForm>()
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [settings, setSettings] = useState<AdminSettingsType | null>(null)

  useEffect(() => {
    api
      .adminSettings()
      .then((value) => {
        setSettings(value)
        form.setFieldsValue({
          site_name: value.site_name,
          max_file_size_gb: Math.round((value.max_file_size / GIB) * 100) / 100,
          free_space_reserve_gb: Math.round((value.free_space_reserve / GIB) * 100) / 100,
          recycle_retention_days: value.recycle_retention_days,
          backup_enabled: value.backup_enabled,
          backup_time: dayjs(`2000-01-01 ${value.backup_time || '02:30'}`),
          backup_keep: value.backup_keep,
          backup_dir: value.backup_dir,
        })
      })
      .catch((err) => {
        if (err instanceof Error) message.error(err.message)
      })
      .finally(() => setLoading(false))
  }, [form, message])

  async function save() {
    try {
      const values = await form.validateFields()
      setSaving(true)
      const updated = await api.updateSettings({
        site_name: values.site_name.trim(),
        max_file_size: Math.round(values.max_file_size_gb * GIB),
        free_space_reserve: Math.round(values.free_space_reserve_gb * GIB),
        recycle_retention_days: values.recycle_retention_days,
        backup_enabled: values.backup_enabled,
        backup_time: values.backup_time.format('HH:mm'),
        backup_keep: values.backup_keep,
        backup_dir: (values.backup_dir || '').trim(),
      })
      setSettings(updated)
      message.success('设置已保存')
      onSaved()
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return <div className="admin-page loading-center"><Spin /></div>
  }

  return (
    <div className="admin-page">
      <div className="page-heading">
        <div>
          <h1>系统设置</h1>
          <p>调整文件大小限制、回收站保留期和自动备份策略。修改后立即生效。</p>
        </div>
        <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={() => void save()}>
          保存设置
        </Button>
      </div>

      <Form form={form} layout="vertical" requiredMark={false}>
        <Row gutter={16}>
          <Col xs={24} xl={14}>
            <Card bordered={false} title="基础设置">
              <Form.Item
                name="site_name"
                label="平台名称"
                rules={[{ required: true, message: '请输入平台名称' }, { max: 40, message: '最多 40 个字符' }]}
              >
                <Input placeholder="例如：办公室文件中心" />
              </Form.Item>
              <Row gutter={16}>
                <Col span={12}>
                  <Form.Item
                    name="max_file_size_gb"
                    label="单个文件上限"
                    rules={[{ required: true, message: '请输入上限' }]}
                    extra="单位 GB，允许范围约 0.001～100 GB"
                  >
                    <InputNumber min={0.001} max={100} step={0.5} precision={3} style={{ width: '100%' }} addonAfter="GB" />
                  </Form.Item>
                </Col>
                <Col span={12}>
                  <Form.Item
                    name="free_space_reserve_gb"
                    label="磁盘保留空间"
                    rules={[{ required: true, message: '请输入保留空间' }]}
                    extra="可用空间低于此值时暂停上传，保障服务运行"
                  >
                    <InputNumber min={0} max={500} step={1} precision={1} style={{ width: '100%' }} addonAfter="GB" />
                  </Form.Item>
                </Col>
              </Row>
              <Form.Item
                name="recycle_retention_days"
                label="回收站建议保留天数"
                rules={[{ required: true, message: '请输入天数' }]}
                extra="仅用于提示管理员。系统不会自动永久删除回收站内容。"
              >
                <InputNumber min={1} max={3650} style={{ width: 220 }} addonAfter="天" />
              </Form.Item>
            </Card>
          </Col>

          <Col xs={24} xl={10}>
            <Card bordered={false} title="备份策略">
              <Form.Item name="backup_enabled" label="启用每日自动备份" valuePropName="checked">
                <Switch checkedChildren="启用" unCheckedChildren="关闭" />
              </Form.Item>
              <Row gutter={16}>
                <Col span={12}>
                  <Form.Item
                    name="backup_time"
                    label="每天开始时间"
                    rules={[{ required: true, message: '请选择时间' }]}
                    extra="按服务器本地时间"
                  >
                    <TimePicker format="HH:mm" minuteStep={5} style={{ width: '100%' }} allowClear={false} />
                  </Form.Item>
                </Col>
                <Col span={12}>
                  <Form.Item
                    name="backup_keep"
                    label="保留成功备份数"
                    rules={[{ required: true, message: '请输入份数' }]}
                  >
                    <InputNumber min={1} max={90} style={{ width: '100%' }} addonAfter="份" />
                  </Form.Item>
                </Col>
              </Row>
              <Form.Item
                name="backup_dir"
                label="备份目录"
                extra="建议使用另一块硬盘、移动硬盘或另一台电脑上的目录；不能位于数据目录内。服务账号必须对此目录有写入权限。"
              >
                <Input placeholder="例如 D:\OfficeBackup 或 \\NAS\OfficeBackup" />
              </Form.Item>
              <Alert
                type="info"
                showIcon
                message="备份与恢复"
                description="备份会包含数据库快照和全部历史文件，并在完成后校验摘要。建议每月至少抽查一次备份记录，并按管理员手册做恢复演练。"
              />
            </Card>
          </Col>
        </Row>
      </Form>

      {settings && (
        <Card bordered={false} title="当前策略说明" style={{ marginTop: 16 }}>
          <Space direction="vertical" size={6}>
            <Text>单个文件最大 {settings.max_file_size >= GIB ? `${(settings.max_file_size / GIB).toFixed(2)} GB` : `${Math.round(settings.max_file_size / 1024 / 1024)} MB`}。</Text>
            <Paragraph type="secondary" style={{ margin: 0 }}>
              历史版本默认不自动清理。空间接近不足时系统会暂停新上传，但已有文件和历史版本仍可下载。
              如需要清理历史版本，可在文件的历史版本页逐个清理，操作会记入日志。
            </Paragraph>
          </Space>
        </Card>
      )}
    </div>
  )
}
