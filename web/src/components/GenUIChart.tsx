import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

type RecordValue = Record<string, unknown>

type ChartSeries = {
  dataKey: string
  label: string
  valuePrefix: string
  valueSuffix: string
}

type ChartData = {
  chartType: string
  data: RecordValue[]
  xKey: string
  nameKey: string
  valueKey: string
  series: ChartSeries[]
  description: string
}

const COLORS = ['#0d0d0d', '#10a37f', '#3b82f6', '#f59e0b', '#8b5cf6', '#ef4444']
const AXIS_COLOR = '#8f8f8f'

function asRecord(value: unknown): RecordValue | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  return value as RecordValue
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function numberValue(value: unknown): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return 0
}

function normalizeChart(content: RecordValue | null): ChartData | null {
  if (!content) return null
  const data = (Array.isArray(content.data) ? content.data : [])
    .map(asRecord)
    .filter((item): item is RecordValue => item !== null)
  if (data.length === 0) return null
  const rawSeries = Array.isArray(content.series) ? content.series : []
  const series: ChartSeries[] = rawSeries
    .map(asRecord)
    .filter((item): item is RecordValue => item !== null)
    .map((item) => ({
      dataKey: stringValue(item.dataKey) || 'value',
      label: stringValue(item.label) || stringValue(item.dataKey) || '数值',
      valuePrefix: stringValue(item.valuePrefix),
      valueSuffix: stringValue(item.valueSuffix),
    }))
  if (series.length === 0) {
    series.push({ dataKey: 'value', label: '数值', valuePrefix: '', valueSuffix: '' })
  }
  return {
    chartType: (stringValue(content.chartType) || 'bar').toLowerCase(),
    data,
    xKey: stringValue(content.xKey) || stringValue(content.nameKey) || 'name',
    nameKey: stringValue(content.nameKey) || 'name',
    valueKey: stringValue(content.valueKey) || series[0].dataKey,
    series,
    description: stringValue(content.description),
  }
}

function formatValue(value: unknown, series: ChartSeries): string {
  const number = numberValue(value)
  const formatted = Number.isInteger(number) ? String(number) : number.toFixed(1)
  return `${series.valuePrefix}${formatted}${series.valueSuffix}`
}

function tooltipStyle() {
  return {
    border: '1px solid #e5e5e5',
    borderRadius: 10,
    boxShadow: '0 4px 16px rgba(0,0,0,0.08)',
    fontSize: 12,
    padding: '8px 10px',
  }
}

function CartesianChart({ chart }: { chart: ChartData }) {
  const isLine = chart.chartType === 'line'
  const ChartRoot = isLine ? LineChart : BarChart
  return (
    <ResponsiveContainer width="100%" height={280}>
      <ChartRoot data={chart.data} margin={{ top: 12, right: 20, left: 4, bottom: 4 }}>
        <CartesianGrid stroke="#ececec" strokeDasharray="3 3" vertical={false} />
        <XAxis
          dataKey={chart.xKey}
          tick={{ fontSize: 11, fill: AXIS_COLOR }}
          axisLine={false}
          tickLine={false}
          interval="preserveStartEnd"
        />
        <YAxis
          tick={{ fontSize: 10.5, fill: AXIS_COLOR }}
          axisLine={false}
          tickLine={false}
          width={54}
          tickFormatter={(value: number | string) => formatValue(value, chart.series[0])}
        />
        <Tooltip
          contentStyle={tooltipStyle()}
          cursor={isLine ? { stroke: '#d6d6d6', strokeDasharray: '3 3' } : { fill: 'rgba(0,0,0,0.03)' }}
        />
        {chart.series.map((series, index) =>
          isLine ? (
            <Line
              key={series.dataKey}
              type="monotone"
              dataKey={series.dataKey}
              name={series.label}
              stroke={COLORS[index % COLORS.length]}
              strokeWidth={2.5}
              dot={{ r: 3, strokeWidth: 2, fill: '#ffffff' }}
              activeDot={{ r: 4 }}
            />
          ) : (
            <Bar
              key={series.dataKey}
              dataKey={series.dataKey}
              name={series.label}
              fill={COLORS[index % COLORS.length]}
              radius={[4, 4, 0, 0]}
              maxBarSize={42}
            />
          ),
        )}
      </ChartRoot>
    </ResponsiveContainer>
  )
}

function PieChartView({ chart }: { chart: ChartData }) {
  const data = chart.data.map((item) => ({
    name: stringValue(item[chart.nameKey]) || stringValue(item[chart.xKey]),
    value: numberValue(item[chart.valueKey]),
  }))
  if (data.every((item) => item.value <= 0)) return null
  return (
    <ResponsiveContainer width="100%" height={280}>
      <PieChart>
        <Pie
          data={data}
          dataKey="value"
          nameKey="name"
          cx="38%"
          cy="50%"
          outerRadius={92}
          paddingAngle={1}
          stroke="#ffffff"
          strokeWidth={2}
        >
          {data.map((entry, index) => (
            <Cell key={entry.name} fill={COLORS[index % COLORS.length]} />
          ))}
        </Pie>
        <Tooltip contentStyle={tooltipStyle()} />
        <Legend
          layout="vertical"
          align="right"
          verticalAlign="middle"
          iconType="circle"
          iconSize={8}
          wrapperStyle={{ fontSize: 12, color: '#5d5d5d', paddingLeft: 12 }}
        />
      </PieChart>
    </ResponsiveContainer>
  )
}

export default function ChartWidget({ data }: { data?: RecordValue }) {
  const chart = normalizeChart(data ?? null)
  if (!chart) {
    return <div className="genui-unimplemented">未实现</div>
  }
  const body =
    chart.chartType === 'bar' || chart.chartType === 'line' ? (
      <CartesianChart chart={chart} />
    ) : chart.chartType === 'pie' ? (
      <PieChartView chart={chart} />
    ) : null
  if (!body) {
    return <div className="genui-unimplemented">未实现</div>
  }
  return (
    <figure className="genui-chart" role="img" aria-label={`${chart.chartType} 图表`}>
      <div className="genui-chart-canvas">{body}</div>
      {chart.description ? <figcaption>{chart.description}</figcaption> : null}
    </figure>
  )
}
