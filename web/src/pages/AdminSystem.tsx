import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Row,
  Space,
  Statistic,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import {
  CloudDownloadOutlined,
  DatabaseOutlined,
  FolderOpenOutlined,
  SafetyCertificateOutlined,
  SyncOutlined,
  TeamOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import dayjs from 'dayjs'
import { api, formatBytes } from '../api'
import type { AdminSettings, BackupRun, PublicSettings, SystemInfo } from '../types'
import { fmtTime } from '../labels'

const { Text, Paragraph } = Typography

export function AdminSystem({
  settings,
  onOpenSettings,
}: {
  settings: PublicSettings
  onOpenSettings: () => void
}) {
  const { message } = App.useApp()
  const [system, setSystem] = useState<SystemInfo | null>(null)
  const [adminSettings, setAdminSettings] = useState<AdminSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [starting, setStarting] = useState(false)

  const load = useCallback(async () => {
    try {
      const [sys, cfg] = await Promise.all([api.system(), api.adminSettings()])
      setSystem(sys)
      setAdminSettings(cfg)
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    void load()
    const timer = setInterval(() => void load(), 10000)
    return () => clearInterval(timer)
  }, [load])

  async function runBackup() {
    setStarting(true)
    try {
      const result = await api.runBackup()
      message.success(result.message)
      setTimeout(() => void load(), 1200)
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setStarting(false)
    }
  }

  const backupDir = adminSettings?.backup_dir ?? settings.backup_dir
  const last = system?.last_backup
  const lastDays = last?.finished_at ? dayjs().diff(dayjs(last.finished_at), 'day') : null
  const latestFailed = system?.recent_backups?.[0]?.status === 'failed'
  const backupWarning = !backupDir
    ? '尚未配置备份目录，当前没有可用的灾备副本。'
    : latestFailed
      ? `最近一次备份失败：${system?.recent_backups?.[0]?.message || '请查看备份记录'}`
      : !last
        ? '尚未成功完成一次备份，请在下方点击“立即备份”并确认结果。'
        : lastDays !== null && lastDays >= 2
          ? `最近一次成功备份已是 ${lastDays} 天前，请检查备份任务或立即手动备份。`
          : ''

  const backupColumns: ColumnsType<BackupRun> = [
    { title: '开始时间', dataIndex: 'started_at', width: 175, render: (value: string) => fmtTime(value) },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      render: (value: string) =>
        value === 'success' ? (
          <Tag color="#587d63">成功</Tag>
        ) : value === 'running' ? (
          <Tag color="#3a79bc">进行中</Tag>
        ) : (
          <Tag color="#a9635d">失败</Tag>
        ),
    },
    { title: '对象数', dataIndex: 'objects', width: 90 },
    { title: '数据量', dataIndex: 'bytes', width: 110, render: (value: number) => (value ? formatBytes(value) : '—') },
    { title: '说明', dataIndex: 'message', ellipsis: true },
    {
      title: '位置',
      dataIndex: 'path',
      ellipsis: true,
      render: (value: string) => <Text type="secondary" style={{ fontSize: 12 }}>{value || '—'}</Text>,
    },
  ]

  return (
    <div className="admin-page">
      <div className="page-heading">
        <div>
          <h1>备份与系统</h1>
          <p>查看存储、服务与备份状态。系统每天自动备份，历史版本不能代替独立备份。</p>
        </div>
        <Space>
          <Button type="primary" icon={<ThunderboltOutlined />} loading={starting || system?.backup_running} onClick={() => void runBackup()}>
            {system?.backup_running ? '备份进行中' : '立即备份'}
          </Button>
          <Button icon={<SyncOutlined />} onClick={() => void load()}>刷新</Button>
        </Space>
      </div>

      {backupWarning && (
        <Alert
          type="warning"
          showIcon
          message="备份提醒"
          description={backupWarning}
          action={
            !backupDir ? (
              <Button size="small" onClick={onOpenSettings}>配置备份</Button>
            ) : undefined
          }
          style={{ marginBottom: 18 }}
        />
      )}
      {system && !system.tls_enabled && (
        <Alert
          type="error"
          showIcon
          message="当前服务运行在 HTTP 明文模式"
          description="仅建议在本机测试时使用。正式部署请启用 HTTPS，避免密码和文件在局域网内被截获。"
          style={{ marginBottom: 18 }}
        />
      )}

      <Row gutter={[16, 16]} className="stat-grid">
        <Col xs={24} sm={12} xl={6}>
          <Card bordered={false} loading={loading}>
            <Statistic
              title="磁盘可用空间"
              value={system ? formatBytes(system.stats.free_space) : '—'}
              prefix={<DatabaseOutlined />}
            />
            <div className="stat-foot">历史版本占用 {system ? formatBytes(system.stats.object_bytes) : '—'}</div>
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card bordered={false} loading={loading}>
            <Statistic
              title="文件 / 文件夹"
              value={system ? `${system.stats.files} / ${system.stats.folders}` : '—'}
              prefix={<FolderOpenOutlined />}
            />
            <div className="stat-foot">共 {system?.stats.versions ?? 0} 个文件版本</div>
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card bordered={false} loading={loading}>
            <Statistic title="可用账号" value={system?.users ?? 0} prefix={<TeamOutlined />} />
            <div className="stat-foot">当前登录会话 {system?.active_sessions ?? 0} 个</div>
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card bordered={false} loading={loading}>
            <Statistic
              title="服务版本"
              value={system?.version || '—'}
              prefix={<SafetyCertificateOutlined />}
            />
            <div className="stat-foot">启动于 {system ? fmtTime(system.started_at) : '—'}</div>
          </Card>
        </Col>
      </Row>

      <Row gutter={[16, 16]} style={{ marginTop: 16 }}>
        <Col xs={24} xl={14}>
          <Card
            bordered={false}
            title="备份状态"
            extra={<Button size="small" onClick={onOpenSettings}>备份设置</Button>}
          >
            <Descriptions column={1} size="small">
              <Descriptions.Item label="自动备份">
                {settings.backup_enabled ? `每天 ${settings.backup_time}` : '已关闭'}
                {`，保留最近 ${settings.backup_keep} 份`}
              </Descriptions.Item>
              <Descriptions.Item label="备份目录">
                {backupDir ? <Text copyable>{backupDir}</Text> : <Text type="danger">未配置</Text>}
              </Descriptions.Item>
              <Descriptions.Item label="最近成功">
                {last ? (
                  <Space wrap>
                    <Tag color="#587d63">成功</Tag>
                    <span>{fmtTime(last.finished_at)}</span>
                    <Text type="secondary">（{last.objects} 个对象，{formatBytes(last.bytes)}）</Text>
                  </Space>
                ) : (
                  <Tag color="#96703f">暂无成功记录</Tag>
                )}
              </Descriptions.Item>
            </Descriptions>
            <Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0, fontSize: 12 }}>
              备份包含数据库一致性快照和全部历史文件。建议目标为另一块硬盘、移动硬盘或另一台电脑，并定期做一次恢复演练。
              恢复窗口取决于备份频率；若备份失败，实际数据丢失窗口会扩大。
            </Paragraph>
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card bordered={false} title="HTTPS 证书">
            {system?.tls_enabled ? (
              <Descriptions column={1} size="small">
                <Descriptions.Item label="状态">
                  <Tag color="#587d63">已启用 HTTPS</Tag>
                  {system.cert.self_signed && <Tag>自签名证书</Tag>}
                </Descriptions.Item>
                <Descriptions.Item label="到期时间">{fmtTime(system.cert.not_after)}</Descriptions.Item>
                <Descriptions.Item label="证书名称">{system.cert.subject}</Descriptions.Item>
                <Descriptions.Item label="SHA-256 指纹">
                  <Text copyable style={{ fontSize: 11, wordBreak: 'break-all' }}>{system.cert.fingerprint}</Text>
                </Descriptions.Item>
              </Descriptions>
            ) : (
              <Text type="danger">未启用 HTTPS</Text>
            )}
            <Space style={{ marginTop: 12 }} wrap>
              {system?.tls_enabled && (
                <Tooltip title="在每台员工电脑上安装此证书到“受信任的根证书颁发机构”，浏览器才不会显示安全警告">
                  <Button icon={<CloudDownloadOutlined />} onClick={() => { window.location.href = '/api/cert' }}>
                    下载客户端证书
                  </Button>
                </Tooltip>
              )}
            </Space>
            <Paragraph type="secondary" style={{ marginTop: 10, marginBottom: 0, fontSize: 12 }}>
              此证书用于让浏览器信任服务器身份。请通过内部可靠渠道分发，并核对指纹。证书到期前需替换并重新分发。
            </Paragraph>
          </Card>
        </Col>
      </Row>

      <Card bordered={false} title="最近备份记录" style={{ marginTop: 16 }}>
        <Table
          rowKey="id"
          size="small"
          columns={backupColumns}
          dataSource={system?.recent_backups || []}
          loading={loading}
          pagination={false}
          locale={{ emptyText: '还没有备份记录' }}
        />
      </Card>

      {system && (
        <Card bordered={false} title="运行信息" style={{ marginTop: 16 }}>
          <Descriptions column={2} size="small">
            <Descriptions.Item label="数据目录"><Text copyable>{system.data_dir}</Text></Descriptions.Item>
            <Descriptions.Item label="服务版本">{system.version}</Descriptions.Item>
            <Descriptions.Item label="监听方式">{system.tls_enabled ? 'HTTPS' : 'HTTP（仅测试）'}</Descriptions.Item>
            <Descriptions.Item label="备份说明">数据库与文件对象分开存储，恢复时需使用配套备份。</Descriptions.Item>
          </Descriptions>
        </Card>
      )}
    </div>
  )
}
