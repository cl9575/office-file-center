import { useEffect, useState } from 'react'
import {
  App,
  Button,
  Descriptions,
  Drawer,
  Empty,
  Popconfirm,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import { DownloadOutlined, HistoryOutlined, RollbackOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { api, downloadUrl, formatBytes } from '../api'
import type { AuditRow, NodeItem, User, VersionItem } from '../types'
import { actionLabel, fmtTime, resultColors, resultLabels } from '../labels'

const { Text } = Typography

export function DetailsDrawer({
  node,
  open,
  user,
  onClose,
  onChanged,
}: {
  node: NodeItem | null
  open: boolean
  user: User
  onClose: () => void
  onChanged: (node?: NodeItem) => void
}) {
  const { message } = App.useApp()
  const [current, setCurrent] = useState<NodeItem | null>(node)
  const [versions, setVersions] = useState<VersionItem[]>([])
  const [audit, setAudit] = useState<AuditRow[]>([])
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState('')

  useEffect(() => {
    setCurrent(node)
  }, [node])

  useEffect(() => {
    if (!open || !node) return
    let alive = true
    setLoading(true)
    Promise.all([
      node.kind === 'file' ? api.versions(node.id) : Promise.resolve([]),
      api.nodeAudit(node.id),
    ])
      .then(([versionList, auditList]) => {
        if (!alive) return
        setVersions(versionList)
        setAudit(auditList)
      })
      .catch((err) => {
        if (alive && err?.message) message.error(err.message)
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [open, node?.id])

  async function reload(target: NodeItem) {
    try {
      const [versionList, auditList] = await Promise.all([
        api.versions(target.id),
        api.nodeAudit(target.id),
      ])
      setVersions(versionList)
      setAudit(auditList)
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    }
  }

  async function restore(version: VersionItem) {
    if (!current) return
    setBusy(version.id)
    try {
      const result = await api.restoreVersion(current.id, version.id, current.current_version_id || '')
      message.success(`已将 V${version.version_no} 恢复为最新版本 V${result.node.version_no}`)
      setCurrent(result.node)
      await reload(result.node)
      onChanged(result.node)
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setBusy('')
    }
  }

  async function purge(version: VersionItem) {
    if (!current) return
    setBusy(version.id)
    try {
      await api.purgeVersion(current.id, version.id)
      message.success(`已清理 V${version.version_no}`)
      await reload(current)
      onChanged(current)
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setBusy('')
    }
  }

  const versionColumns: ColumnsType<VersionItem> = [
    {
      title: '版本',
      dataIndex: 'version_no',
      width: 90,
      render: (value: number, row) => (
        <Space size={6}>
          <Text strong>V{value}</Text>
          {row.is_current && <Tag color="#587d63">当前</Tag>}
        </Space>
      ),
    },
    { title: '上传人', dataIndex: 'created_by_name', width: 120 },
    { title: '时间', dataIndex: 'created_at', width: 170, render: (value: string) => fmtTime(value) },
    {
      title: '操作',
      key: 'actions',
      width: 210,
      render: (_, row) => (
        <Space size={4}>
          <Tooltip title="下载此版本">
            <Button
              type="link"
              size="small"
              icon={<DownloadOutlined />}
              onClick={() => {
                window.location.href = downloadUrl(current!.id, row.id)
              }}
            >
              下载
            </Button>
          </Tooltip>
          {(user.role === 'admin' || user.role === 'editor') && (
            <Popconfirm
              title={`把 V${row.version_no} 恢复为最新版本？`}
              description="当前内容会保留为一个历史版本，不会丢失。"
              onConfirm={() => void restore(row)}
            >
              <Button
                type="link"
                size="small"
                icon={<RollbackOutlined />}
                loading={busy === row.id}
                disabled={row.is_current}
              >
                恢复
              </Button>
            </Popconfirm>
          )}
          {user.role === 'admin' && !row.is_current && (
            <Popconfirm
              title={`清理 V${row.version_no}？`}
              description="清理后该版本不可从系统下载，操作会记录在日志中。"
              okButtonProps={{ danger: true }}
              onConfirm={() => void purge(row)}
            >
              <Button type="link" size="small" danger loading={busy === row.id}>
                清理
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
    { title: '大小', dataIndex: 'size', width: 100, render: (value: number) => formatBytes(value) },
  ]

  const auditColumns: ColumnsType<AuditRow> = [
    { title: '时间', dataIndex: 'created_at', width: 165, render: fmtTime },
    { title: '操作人', dataIndex: 'actor_name', width: 110 },
    {
      title: '操作',
      dataIndex: 'action',
      width: 120,
      render: (value: string) => <Tag>{actionLabel(value)}</Tag>,
    },
    { title: '详情', dataIndex: 'detail', ellipsis: true },
    {
      title: '结果',
      dataIndex: 'result',
      width: 80,
      render: (value: string) => <Tag color={resultColors[value]}>{resultLabels[value] || value}</Tag>,
    },
  ]

  const items = [
    {
      key: 'info',
      label: '文件信息',
      children: current ? (
        <Space direction="vertical" size={20} style={{ width: '100%' }}>
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="名称">{current.name}</Descriptions.Item>
            <Descriptions.Item label="位置">{current.path || '/'}</Descriptions.Item>
            <Descriptions.Item label="类型">{current.kind === 'folder' ? '文件夹' : '文件'}</Descriptions.Item>
            {current.kind === 'file' && (
              <>
                <Descriptions.Item label="大小">{formatBytes(current.size)}</Descriptions.Item>
                <Descriptions.Item label="当前版本">V{current.version_no}</Descriptions.Item>
                <Descriptions.Item label="摘要">
                  <Text copyable={{ text: current.sha256 }} code style={{ fontSize: 11 }}>
                    {current.sha256}
                  </Text>
                </Descriptions.Item>
              </>
            )}
            <Descriptions.Item label="更新人">{current.updated_by}</Descriptions.Item>
            <Descriptions.Item label="更新时间">{fmtTime(current.updated_at)}</Descriptions.Item>
          </Descriptions>
          {current.kind === 'file' && (
            <Button
              type="primary"
              icon={<DownloadOutlined />}
              onClick={() => {
                window.location.href = downloadUrl(current.id)
              }}
            >
              下载当前版本
            </Button>
          )}
        </Space>
      ) : null,
    },
    ...(current?.kind === 'file'
      ? [
          {
            key: 'versions',
            label: (
              <span>
                <HistoryOutlined /> 历史版本 ({versions.length})
              </span>
            ),
            children: (
              <Table
                rowKey="id"
                size="small"
                loading={loading}
                columns={versionColumns}
                dataSource={versions}
                pagination={false}
                scroll={{ x: 720 }}
                locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无历史版本" /> }}
              />
            ),
          },
        ]
      : []),
    {
      key: 'audit',
      label: '操作记录',
      children: (
        <Table
          rowKey="seq"
          size="small"
          loading={loading}
          columns={auditColumns}
          dataSource={audit}
          pagination={false}
          scroll={{ x: 640 }}
          locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无操作记录" /> }}
        />
      ),
    },
  ]

  return (
    <Drawer
      title={current?.kind === 'folder' ? '文件夹详情' : '文件详情'}
      width={760}
      rootClassName="details-drawer"
      open={open}
      onClose={onClose}
      destroyOnClose
    >
      <Tabs items={items} />
    </Drawer>
  )
}
