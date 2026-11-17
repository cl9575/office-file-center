import { useEffect, useState } from 'react'
import { Alert, App, Button, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import { ClearOutlined, DeleteOutlined, UndoOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import dayjs from 'dayjs'
import { api } from '../api'
import type { NodeItem, PublicSettings } from '../types'
import { fmtTime } from '../labels'

const { Text } = Typography

export function AdminRecycle({ settings }: { settings: PublicSettings }) {
  const { message, modal } = App.useApp()
  const [items, setItems] = useState<NodeItem[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [clearing, setClearing] = useState(false)

  async function load() {
    setLoading(true)
    try {
      setItems(await api.recycle())
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function restore(row: NodeItem) {
    if (!row.delete_batch) return
    setBusy(row.delete_batch)
    try {
      const result = await api.restoreRecycle(row.delete_batch)
      message.success(`已恢复 ${result.restored} 项`)
      await load()
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setBusy('')
    }
  }

  function purge(row: NodeItem) {
    if (!row.delete_batch) return
    modal.confirm({
      title: `永久删除「${row.name}」？`,
      content:
        '该操作不可撤销，会删除回收站中的内容和相关历史版本。请确认这些文件不再需要；若需要保留，请先恢复或确保已有独立备份。',
      okText: '确认永久删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        setBusy(row.delete_batch!)
        try {
          const result = await api.purgeRecycle(row.delete_batch!)
          message.success(`已永久删除 ${result.purged} 项`)
          await load()
        } catch (err) {
          if (err instanceof Error) message.error(err.message)
        } finally {
          setBusy('')
        }
      },
    })
  }

  function purgeAll() {
    if (!items.length || clearing) return
    modal.confirm({
      title: '清空回收站？',
      content: `回收站中的 ${items.length} 个顶层项目及其所有子项、历史版本将被永久删除。此操作不可撤销，请确认这些内容不再需要，或已有独立备份。`,
      okText: '全部永久删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        setClearing(true)
        try {
          const result = await api.purgeAllRecycle()
          message.success(result.purged > 0 ? `已永久删除 ${result.purged} 项` : '回收站已经是空的')
          await load()
        } catch (err) {
          if (err instanceof Error) message.error(err.message)
        } finally {
          setClearing(false)
        }
      },
    })
  }

  function remaining(deletedAt?: string): { text: string; color: string } {
    if (!deletedAt) return { text: '—', color: 'default' }
    const days = dayjs(deletedAt).add(settings.recycle_retention_days, 'day').diff(dayjs(), 'day', true)
    if (days <= 0) return { text: '已超过建议保留期', color: '#a9635d' }
    return { text: `约 ${Math.ceil(days)} 天`, color: days <= 7 ? '#96703f' : 'default' }
  }

  const columns: ColumnsType<NodeItem> = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (value: string, row) => (
        <Space>
          <Tag>{row.kind === 'folder' ? '目录' : '文件'}</Tag>
          <Text strong>{value}</Text>
        </Space>
      ),
    },
    { title: '原位置', dataIndex: 'path', ellipsis: true, render: (value: string) => <span className="muted">{value}</span> },
    { title: '删除人', dataIndex: 'deleted_by', width: 110 },
    { title: '删除时间', dataIndex: 'deleted_at', width: 175, render: (value: string) => fmtTime(value) },
    {
      title: '建议保留',
      key: 'remaining',
      width: 150,
      render: (_, row) => {
        const value = remaining(row.deleted_at)
        return <Tag color={value.color}>{value.text}</Tag>
      },
    },
    {
      title: '操作',
      key: 'actions',
      width: 190,
      render: (_, row) => (
        <Space size={0}>
          <Popconfirm
            title="恢复此内容？"
            description="如果原位置已存在同名项目，恢复会被拒绝，请先处理冲突。"
            onConfirm={() => void restore(row)}
          >
            <Button type="link" size="small" icon={<UndoOutlined />} disabled={clearing} loading={busy === row.delete_batch}>
              恢复
            </Button>
          </Popconfirm>
          <Button
            type="link"
            size="small"
            danger
            icon={<DeleteOutlined />}
            disabled={clearing}
            loading={busy === row.delete_batch}
            onClick={() => purge(row)}
          >
            永久删除
          </Button>
        </Space>
      ),
    },
  ]

  return (
    <div className="admin-page">
      <div className="page-heading">
        <div>
          <h1>回收站</h1>
          <p>普通删除会先进入这里。建议保留 {settings.recycle_retention_days} 天，确认无用后再永久清理。</p>
        </div>
        <Button
          danger
          icon={<ClearOutlined />}
          disabled={!items.length || clearing || !!busy}
          loading={clearing}
          onClick={purgeAll}
        >
          清空回收站
        </Button>
      </div>
      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 18 }}
        message="永久删除不可撤销"
        description="历史版本不等于备份。请确保重要文件已由系统备份到独立硬盘或另一台电脑后，再清理回收站。系统不会自动永久删除回收站内容。"
      />
      <Table
        rowKey={(row) => row.delete_batch || row.id}
        columns={columns}
        dataSource={items}
        loading={loading}
        pagination={false}
        locale={{ emptyText: '回收站是空的' }}
      />
    </div>
  )
}
