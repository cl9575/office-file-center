import { useEffect, useState } from 'react'
import {
  App,
  Button,
  Card,
  DatePicker,
  Form,
  Input,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import { DownloadOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { api, type AuditParams } from '../api'
import type { AuditRow } from '../types'
import { actionLabel, actionLabels, fmtTime, resultColors, resultLabels } from '../labels'

const { Text } = Typography
const { RangePicker } = DatePicker

interface AuditForm {
  actor?: string
  action?: string
  result?: string
  q?: string
  range?: [Dayjs, Dayjs]
}

export function AdminAudit() {
  const { message } = App.useApp()
  const [form] = Form.useForm<AuditForm>()
  const [items, setItems] = useState<AuditRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [params, setParams] = useState<AuditParams>({})
  const [loading, setLoading] = useState(true)
  const [exporting, setExporting] = useState(false)

  useEffect(() => {
    let alive = true
    setLoading(true)
    api
      .audit({ ...params, page, page_size: pageSize })
      .then((result) => {
        if (!alive) return
        setItems(result.items)
        setTotal(result.total)
      })
      .catch((err) => {
        if (alive && err instanceof Error) message.error(err.message)
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
  }, [params, page, pageSize, message])

  function search(values: AuditForm) {
    setParams({
      actor: values.actor?.trim() || undefined,
      action: values.action || undefined,
      result: values.result || undefined,
      q: values.q?.trim() || undefined,
      from: values.range?.[0]?.startOf('day').toISOString(),
      to: values.range?.[1]?.endOf('day').toISOString(),
    })
    setPage(1)
  }

  async function exportCsv() {
    setExporting(true)
    try {
      const res = await fetch(api.auditCsvUrl(params), { credentials: 'same-origin' })
      if (!res.ok) throw new Error('导出失败，请稍后重试')
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `操作日志-${dayjs().format('YYYYMMDD-HHmm')}.csv`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
      message.success('日志已导出')
    } catch (err) {
      if (err instanceof Error) message.error(err.message)
    } finally {
      setExporting(false)
    }
  }

  const columns: ColumnsType<AuditRow> = [
    { title: '时间', dataIndex: 'created_at', width: 175, render: (value: string) => fmtTime(value) },
    { title: '操作人', dataIndex: 'actor_name', width: 110, ellipsis: true },
    {
      title: '操作',
      dataIndex: 'action',
      width: 130,
      render: (value: string) => <Tag color="#3a79bc">{actionLabel(value)}</Tag>,
    },
    {
      title: '对象',
      key: 'target',
      width: 240,
      ellipsis: true,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Text ellipsis>{row.target_name || '—'}</Text>
          {row.target_path && <Text type="secondary" style={{ fontSize: 12 }} ellipsis>{row.target_path}</Text>}
        </Space>
      ),
    },
    {
      title: '详情',
      dataIndex: 'detail',
      ellipsis: true,
      render: (value: string) => (
        <Tooltip title={value} placement="topLeft">
          <span>{value || '—'}</span>
        </Tooltip>
      ),
    },
    {
      title: '结果',
      dataIndex: 'result',
      width: 90,
      render: (value: string) => <Tag color={resultColors[value]}>{resultLabels[value] || value}</Tag>,
    },
    { title: '来源 IP', dataIndex: 'ip', width: 130, render: (value: string) => <Text type="secondary">{value || '—'}</Text> },
  ]

  const actionOptions = Object.entries(actionLabels).map(([value, label]) => ({ value, label }))

  return (
    <div className="admin-page">
      <div className="page-heading">
        <div>
          <h1>操作日志</h1>
          <p>记录上传、下载、更新、删除、账号、权限和备份等操作，便于追溯。</p>
        </div>
        <Button icon={<DownloadOutlined />} loading={exporting} onClick={() => void exportCsv()}>
          导出 CSV
        </Button>
      </div>

      <Card className="filter-card" bordered={false}>
        <Form form={form} layout="inline" onFinish={search}>
          <Form.Item name="actor" label="操作人">
            <Input placeholder="操作人姓名" allowClear style={{ width: 150 }} />
          </Form.Item>
          <Form.Item name="action" label="操作">
            <Select
              showSearch
              allowClear
              placeholder="全部操作"
              style={{ width: 170 }}
              options={actionOptions}
              optionFilterProp="label"
            />
          </Form.Item>
          <Form.Item name="result" label="结果">
            <Select
              allowClear
              placeholder="全部"
              style={{ width: 110 }}
              options={[
                { value: 'success', label: '成功' },
                { value: 'failure', label: '失败' },
              ]}
            />
          </Form.Item>
          <Form.Item name="q" label="关键词">
            <Input placeholder="对象名称、路径或详情" allowClear style={{ width: 190 }} />
          </Form.Item>
          <Form.Item name="range" label="日期">
            <RangePicker style={{ width: 240 }} />
          </Form.Item>
          <Form.Item>
            <Space>
              <Button type="primary" htmlType="submit" icon={<SearchOutlined />}>查询</Button>
              <Button
                icon={<ReloadOutlined />}
                onClick={() => {
                  form.resetFields()
                  setParams({})
                  setPage(1)
                }}
              >
                重置
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>

      <Table
        rowKey="seq"
        columns={columns}
        dataSource={items}
        loading={loading}
        scroll={{ x: 1100 }}
        pagination={{
          current: page,
          pageSize,
          total,
          showSizeChanger: true,
          pageSizeOptions: [20, 50, 100, 200],
          showTotal: (count) => `共 ${count} 条记录`,
          onChange: (nextPage, nextSize) => {
            if (nextSize !== pageSize) {
              setPageSize(nextSize)
              setPage(1)
            } else {
              setPage(nextPage)
            }
          },
        }}
        locale={{ emptyText: '没有匹配的操作记录' }}
      />
    </div>
  )
}
