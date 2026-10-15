package requestdb

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// stackLimit 是堆叠趋势单独显示的维度取值数，其余合并为其他
const stackLimit = 8

// recentWindow 是当前吞吐的统计窗口
const recentWindow = 5 * time.Minute

// Dimensions 是用量与请求记录可筛选、分组的维度，同时是 requests、hours、days 表的列名
var Dimensions = []string{"model", "account", "api_key", "protocol", "state", "quota_group"}

// usageDimensions 是报告分项包含的维度，比筛选维度多出 HTTP 状态码
var usageDimensions = append(slices.Clone(Dimensions), "status")

// UsageQuery 选择用量统计的时间范围、分桶、时区、筛选与堆叠维度
type UsageQuery struct {
	From     time.Time
	To       time.Time
	Bucket   time.Duration
	Location *time.Location
	// Filters 按维度限定取值，同一维度的多个取值为或关系
	Filters map[string][]string
	Stack   string
}

// UsageLatency 汇总一组请求的平均值与分位耗时
type UsageLatency struct {
	AvgMS float64 `json:"avg_ms"`
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
}

// UsageStats 汇总一组请求的结果、token 与耗时；成功包含工具调用、输出上限与上游终止，耗时只统计成功请求
type UsageStats struct {
	Requests        int64 `json:"requests"`
	Succeeded       int64 `json:"succeeded"`
	Failed          int64 `json:"failed"`
	Canceled        int64 `json:"canceled"`
	RateLimited     int64 `json:"rate_limited"`
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	ReplyTokens     int64 `json:"reply_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
	// CostUSD 是有单价请求的估算美元费用之和
	CostUSD float64 `json:"cost_usd"`
	// Unpriced 是消耗了 token 但模型没有单价的请求数
	Unpriced   int64        `json:"unpriced"`
	Duration   UsageLatency `json:"duration"`
	FirstEvent UsageLatency `json:"first_event"`
	QueueAvgMS float64      `json:"queue_avg_ms"`
	LastAt     *time.Time   `json:"last_at,omitempty"`
}

// UsageBucket 是一个时间分桶的汇总
type UsageBucket struct {
	At time.Time `json:"at"`
	UsageStats
}

// UsageGroup 是一个维度取值的汇总
type UsageGroup struct {
	Key string `json:"key"`
	UsageStats
}

// UsagePair 是账户与模型组合的汇总
type UsagePair struct {
	Account string `json:"account"`
	Model   string `json:"model"`
	UsageStats
}

// UsageSeries 是堆叠趋势中一个维度取值逐桶的请求数与 token，Other 合并排名之外的取值
type UsageSeries struct {
	Key      string  `json:"key"`
	Other    bool    `json:"other,omitempty"`
	Requests []int64 `json:"requests"`
	Tokens   []int64 `json:"tokens"`
}

// UsageRecent 是最近几分钟的请求与 token，用于计算当前吞吐
type UsageRecent struct {
	Minutes  int   `json:"minutes"`
	Requests int64 `json:"requests"`
	Tokens   int64 `json:"tokens"`
}

// UsageReport 是用量看板的汇总、上一周期、当前吞吐、逐桶趋势、堆叠序列、分项与筛选候选
type UsageReport struct {
	From          time.Time               `json:"from"`
	To            time.Time               `json:"to"`
	BucketSeconds int64                   `json:"bucket_seconds"`
	GeneratedAt   time.Time               `json:"generated_at"`
	LatestAt      *time.Time              `json:"latest_at,omitempty"`
	Totals        UsageStats              `json:"totals"`
	Previous      UsageStats              `json:"previous"`
	Recent        UsageRecent             `json:"recent"`
	Buckets       []UsageBucket           `json:"buckets"`
	StackBy       string                  `json:"stack_by"`
	Series        []UsageSeries           `json:"series"`
	Groups        map[string][]UsageGroup `json:"groups"`
	Pairs         []UsagePair             `json:"pairs"`
	Options       map[string][]string     `json:"options"`
}

// rollupKey 是汇总表的主键
type rollupKey struct {
	start      int64
	model      string
	account    string
	apiKey     string
	protocol   string
	state      string
	quotaGroup string
	status     int
}

// entry 是参与汇总的一条原始记录或一组同键汇总
type entry struct {
	time            int64
	model           string
	account         string
	apiKey          string
	protocol        string
	state           string
	quotaGroup      string
	status          int
	requests        int64
	inputTokens     int64
	cachedTokens    int64
	reasoningTokens int64
	replyTokens     int64
	durationMS      int64
	firstEventMS    int64
	firstEvents     int64
	queueMS         int64
	costUSD         float64
	unpriced        int64
	duration        counts
	firstEvent      counts
	lastTime        int64
}

// keyOf 返回条目在指定分段起点下的汇总键
func keyOf(start int64, value *entry) rollupKey {
	return rollupKey{
		start: start, model: value.model, account: value.account, apiKey: value.apiKey, protocol: value.protocol,
		state: value.state, quotaGroup: value.quotaGroup, status: value.status,
	}
}

// collect 把条目累加到同键汇总，首次出现时保存副本
func collect(target map[rollupKey]*entry, key rollupKey, value *entry) {
	if current := target[key]; current != nil {
		current.combine(value)
		return
	}
	copied := value.clone()
	copied.time = key.start
	target[key] = copied
}

// rowEntry 把一条请求记录转换为汇总条目
func rowEntry(row *Row) *entry {
	value := &entry{
		time: row.Time.UnixMilli(), model: row.Model, account: row.Account, apiKey: row.APIKey, protocol: row.Protocol,
		state: row.State, quotaGroup: row.QuotaGroup, status: row.Status, requests: 1, inputTokens: row.InputTokens,
		cachedTokens: row.CachedTokens, reasoningTokens: row.ReasoningTokens, replyTokens: row.ReplyTokens,
		durationMS: row.DurationMS, queueMS: row.QueueMS, duration: single(row.DurationMS), lastTime: row.Time.UnixMilli(),
	}
	value.costUSD, value.unpriced = cost(row.CostUSD)
	if row.FirstEventMS > 0 {
		value.firstEventMS, value.firstEvents, value.firstEvent = row.FirstEventMS, 1, single(row.FirstEventMS)
	}
	return value
}

// cost 把记录的估算费用转换为汇总的费用与未定价请求数
func cost(value *float64) (float64, int64) {
	if value == nil {
		return 0, 1
	}
	return *value, 0
}

// clone 返回不与扫描缓冲共享分布的副本
func (value *entry) clone() *entry {
	copied := *value
	copied.duration, copied.firstEvent = slices.Clone(value.duration), slices.Clone(value.firstEvent)
	return &copied
}

// combine 把同一汇总键的另一条目累加到当前条目
func (value *entry) combine(other *entry) {
	value.requests += other.requests
	value.inputTokens += other.inputTokens
	value.cachedTokens += other.cachedTokens
	value.reasoningTokens += other.reasoningTokens
	value.replyTokens += other.replyTokens
	value.durationMS += other.durationMS
	value.firstEventMS += other.firstEventMS
	value.firstEvents += other.firstEvents
	value.queueMS += other.queueMS
	value.costUSD += other.costUSD
	value.unpriced += other.unpriced
	value.duration = mergeCounts(value.duration, other.duration)
	value.firstEvent = mergeCounts(value.firstEvent, other.firstEvent)
	value.lastTime = max(value.lastTime, other.lastTime)
}

// dimension 返回条目在指定维度上的取值
func (value *entry) dimension(name string) string {
	switch name {
	case "model":
		return value.model
	case "account":
		return value.account
	case "api_key":
		return value.apiKey
	case "protocol":
		return value.protocol
	case "state":
		return value.state
	case "quota_group":
		return value.quotaGroup
	case "status":
		return strconv.Itoa(value.status)
	}
	return ""
}

// aggregate 累计一组条目
type aggregate struct {
	requests        int64
	succeeded       int64
	failed          int64
	canceled        int64
	rateLimited     int64
	inputTokens     int64
	cachedTokens    int64
	reasoningTokens int64
	replyTokens     int64
	durationMS      int64
	firstEventMS    int64
	firstEvents     int64
	queueMS         int64
	costUSD         float64
	unpriced        int64
	duration        dense
	firstEvent      dense
	lastTime        int64
}

// add 累计一个条目
func (total *aggregate) add(value *entry) {
	total.requests += value.requests
	switch value.state {
	case StateFailed:
		total.failed += value.requests
	case StateCancelled:
		total.canceled += value.requests
	default:
		total.succeeded += value.requests
		total.durationMS += value.durationMS
		total.firstEventMS += value.firstEventMS
		total.firstEvents += value.firstEvents
		total.duration.add(value.duration)
		total.firstEvent.add(value.firstEvent)
	}
	if value.status == 429 {
		total.rateLimited += value.requests
	}
	total.inputTokens += value.inputTokens
	total.cachedTokens += value.cachedTokens
	total.reasoningTokens += value.reasoningTokens
	total.replyTokens += value.replyTokens
	total.queueMS += value.queueMS
	total.costUSD += value.costUSD
	total.unpriced += value.unpriced
	total.lastTime = max(total.lastTime, value.lastTime)
}

// stats 生成公开的汇总结构，总 token 为输入、思考与回复之和
func (total *aggregate) stats() UsageStats {
	stats := UsageStats{
		Requests: total.requests, Succeeded: total.succeeded, Failed: total.failed, Canceled: total.canceled,
		RateLimited: total.rateLimited, InputTokens: total.inputTokens, CachedTokens: total.cachedTokens,
		ReasoningTokens: total.reasoningTokens, ReplyTokens: total.replyTokens,
		TotalTokens: total.inputTokens + total.reasoningTokens + total.replyTokens, CostUSD: total.costUSD, Unpriced: total.unpriced,
	}
	if total.succeeded > 0 {
		stats.Duration = latency(total.durationMS, total.succeeded, &total.duration)
	}
	if total.firstEvents > 0 {
		stats.FirstEvent = latency(total.firstEventMS, total.firstEvents, &total.firstEvent)
	}
	if total.requests > 0 {
		stats.QueueAvgMS = float64(total.queueMS) / float64(total.requests)
	}
	if total.lastTime > 0 {
		last := time.UnixMilli(total.lastTime).UTC()
		stats.LastAt = &last
	}
	return stats
}

// latency 由总和、数量与分布计算平均值与分位数
func latency(sum int64, count int64, distribution *dense) UsageLatency {
	values := distribution.quantiles(0.5, 0.95, 0.99)
	return UsageLatency{AvgMS: float64(sum) / float64(count), P50MS: values[0], P95MS: values[1], P99MS: values[2]}
}

// source 是统计段的读取来源
type source int

const (
	fromRequests source = iota
	fromHours
	fromDays
)

// table 返回来源的表名与时间列
func (from source) table() (string, string) {
	switch from {
	case fromHours:
		return "hours", "start"
	case fromDays:
		return "days", "start"
	}
	return "requests", "time"
}

// segment 是从同一来源读取的连续时间段
type segment struct {
	from   int64
	to     int64
	source source
}

// localDay 返回包含该时间的服务器本地日起止毫秒；夏令时跳过零点时以 time.Date 归一化后的时刻为界
func localDay(at int64) (int64, int64) {
	year, month, day := time.UnixMilli(at).In(time.Local).Date()
	midnight := func(offset int) int64 { return time.Date(year, month, day+offset, 0, 0, 0, 0, time.Local).UnixMilli() }
	if next := midnight(1); at >= next {
		return next, midnight(2)
	}
	return midnight(0), midnight(1)
}

// bucketStarts 返回覆盖 [from, to) 的对齐分桶起点；按天分桶按时区自然日对齐，其余按查询起点的时区偏移对齐
func bucketStarts(from, to time.Time, size time.Duration, location *time.Location) []int64 {
	var starts []int64
	if size >= 24*time.Hour {
		year, month, day := from.In(location).Date()
		for offset := 0; ; offset++ {
			start := time.Date(year, month, day+offset, 0, 0, 0, 0, location)
			if !start.Before(to) {
				return starts
			}
			starts = append(starts, start.UnixMilli())
		}
	}
	_, offset := from.In(location).Zone()
	step, shift := size.Milliseconds(), int64(offset)*1000
	for at := floorDiv(from.UnixMilli()+shift, step)*step - shift; at < to.UnixMilli(); at += step {
		starts = append(starts, at)
	}
	return starts
}

// bucketIndex 返回时间所在分桶的下标
func bucketIndex(starts []int64, at int64) int {
	return sort.Search(len(starts), func(index int) bool { return starts[index] > at }) - 1
}

// sameBucket 判断 [from, to) 是否落在单个分桶内，没有分桶时视为同一分桶
func sameBucket(starts []int64, from, to int64) bool {
	return starts == nil || bucketIndex(starts, from) == bucketIndex(starts, to-1)
}

// appendSegment 追加一段，与前一段同源且相接时合并
func appendSegment(segments []segment, part segment) []segment {
	if count := len(segments); count > 0 && segments[count-1].source == part.source && segments[count-1].to == part.from {
		segments[count-1].to = part.to
		return segments
	}
	return append(segments, part)
}

// plan 把 [from, to) 切成连续段：完整落在范围与单个分桶内的服务器本地日读取日汇总，其余部分按 planHours 切分
func plan(from, to int64, starts []int64) []segment {
	var segments []segment
	for start, end := localDay(from); start < to; start, end = localDay(end) {
		if start >= from && end <= to && sameBucket(starts, start, end) {
			segments = appendSegment(segments, segment{from: start, to: end, source: fromDays})
			continue
		}
		for _, part := range planHours(max(start, from), min(end, to), starts) {
			segments = appendSegment(segments, part)
		}
	}
	return segments
}

// planHours 把 [from, to) 切成连续段：完整落在范围与单个分桶内的整小时读取小时汇总，其余读取原始记录
func planHours(from, to int64, starts []int64) []segment {
	var segments []segment
	for hour := floorDiv(from, hourMS) * hourMS; hour < to; hour += hourMS {
		end, part := hour+hourMS, segment{from: max(hour, from), to: min(hour+hourMS, to)}
		if hour >= from && end <= to && sameBucket(starts, hour, end) {
			part.source = fromHours
		}
		segments = appendSegment(segments, part)
	}
	return segments
}

// filterSQL 返回维度筛选的 SQL 条件与参数，维度名同时是三张表的列名
func filterSQL(filters map[string][]string) (string, []any) {
	var clause strings.Builder
	var args []any
	for _, dimension := range Dimensions {
		values := filters[dimension]
		if len(values) == 0 {
			continue
		}
		clause.WriteString(" AND " + dimension + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")")
		for _, value := range values {
			args = append(args, value)
		}
	}
	return clause.String(), args
}

// scan 读取一个时间段内符合筛选的条目；visit 收到的条目在返回后会被复用
func (store *Store) scan(ctx context.Context, part segment, filters map[string][]string, visit func(*entry)) error {
	clause, filterArgs := filterSQL(filters)
	args := append([]any{part.from, part.to}, filterArgs...)
	table, column := part.source.table()
	value := &entry{}
	if part.source != fromRequests {
		rows, err := store.db.QueryContext(ctx, `SELECT start, model, account, api_key, protocol, state, quota_group, status, requests,
 input_tokens, cached_tokens, reasoning_tokens, reply_tokens, duration_ms, first_event_ms, first_events, queue_ms,
 cost_usd, unpriced, duration_hist, first_event_hist, last_time FROM `+table+` WHERE `+column+` >= ? AND `+column+` < ?`+clause, args...)
		if err != nil {
			return err
		}
		var durations, firstEvents []byte
		for rows.Next() {
			if err := rows.Scan(&value.time, &value.model, &value.account, &value.apiKey, &value.protocol, &value.state,
				&value.quotaGroup, &value.status, &value.requests, &value.inputTokens, &value.cachedTokens, &value.reasoningTokens,
				&value.replyTokens, &value.durationMS, &value.firstEventMS, &value.firstEvents, &value.queueMS, &value.costUSD,
				&value.unpriced, &durations,
				&firstEvents, &value.lastTime); err != nil {
				return errors.Join(err, rows.Close())
			}
			if value.duration, err = decodeCounts(durations); err != nil {
				return errors.Join(err, rows.Close())
			}
			if value.firstEvent, err = decodeCounts(firstEvents); err != nil {
				return errors.Join(err, rows.Close())
			}
			visit(value)
		}
		return errors.Join(rows.Err(), rows.Close())
	}
	rows, err := store.db.QueryContext(ctx, `SELECT time, model, account, api_key, protocol, state, quota_group, status,
 duration_ms, first_event_ms, queue_ms, input_tokens, cached_tokens, reasoning_tokens, reply_tokens, cost_usd
 FROM requests WHERE time >= ? AND time < ?`+clause, args...)
	if err != nil {
		return err
	}
	var duration, firstEvent [1]bucketCount
	var costUSD sql.NullFloat64
	value.requests = 1
	for rows.Next() {
		if err := rows.Scan(&value.time, &value.model, &value.account, &value.apiKey, &value.protocol, &value.state,
			&value.quotaGroup, &value.status, &value.durationMS, &value.firstEventMS, &value.queueMS, &value.inputTokens,
			&value.cachedTokens, &value.reasoningTokens, &value.replyTokens, &costUSD); err != nil {
			return errors.Join(err, rows.Close())
		}
		value.lastTime = value.time
		value.costUSD, value.unpriced = costUSD.Float64, 0
		if !costUSD.Valid {
			value.unpriced = 1
		}
		duration[0] = bucketCount{bucket: histogramBucket(value.durationMS), count: 1}
		value.duration, value.firstEvent, value.firstEvents = duration[:], nil, 0
		if value.firstEventMS > 0 {
			firstEvent[0] = bucketCount{bucket: histogramBucket(value.firstEventMS), count: 1}
			value.firstEvent, value.firstEvents = firstEvent[:], 1
		} else {
			value.firstEventMS = 0
		}
		visit(value)
	}
	return errors.Join(rows.Err(), rows.Close())
}

// Usage 返回时间范围内的汇总、上一周期、当前吞吐、逐桶趋势、堆叠序列、分项与筛选候选
func (store *Store) Usage(ctx context.Context, query UsageQuery) (UsageReport, error) {
	location := query.Location
	if location == nil {
		location = time.UTC
	}
	now := time.Now()
	from, to := query.From.UnixMilli(), query.To.UnixMilli()
	starts := bucketStarts(query.From, query.To, query.Bucket, location)
	totals := &aggregate{}
	buckets := make([]*aggregate, len(starts))
	for index := range buckets {
		buckets[index] = &aggregate{}
	}
	groups := map[string]map[string]*aggregate{}
	for _, dimension := range usageDimensions {
		groups[dimension] = map[string]*aggregate{}
	}
	pairs := map[[2]string]*aggregate{}
	stack := map[string][][2]int64{}
	add := func(target map[string]*aggregate, key string, value *entry) {
		if target[key] == nil {
			target[key] = &aggregate{}
		}
		target[key].add(value)
	}
	visit := func(value *entry) {
		index := bucketIndex(starts, value.time)
		totals.add(value)
		buckets[index].add(value)
		for _, dimension := range usageDimensions {
			add(groups[dimension], value.dimension(dimension), value)
		}
		pair := [2]string{value.account, value.model}
		if pairs[pair] == nil {
			pairs[pair] = &aggregate{}
		}
		pairs[pair].add(value)
		key := value.dimension(query.Stack)
		if stack[key] == nil {
			stack[key] = make([][2]int64, len(starts))
		}
		stack[key][index][0] += value.requests
		stack[key][index][1] += value.inputTokens + value.reasoningTokens + value.replyTokens
	}
	segments := plan(from, to, starts)
	for _, part := range segments {
		if err := store.scan(ctx, part, query.Filters, visit); err != nil {
			return UsageReport{}, err
		}
	}
	previous := &aggregate{}
	for _, part := range plan(from-(to-from), from, nil) {
		if err := store.scan(ctx, part, query.Filters, previous.add); err != nil {
			return UsageReport{}, err
		}
	}
	recent := &aggregate{}
	if err := store.scan(ctx, segment{from: now.Add(-recentWindow).UnixMilli(), to: now.UnixMilli() + 1}, query.Filters, recent.add); err != nil {
		return UsageReport{}, err
	}
	report := UsageReport{
		From: query.From.UTC(), To: query.To.UTC(), BucketSeconds: int64(query.Bucket / time.Second), GeneratedAt: now.UTC(),
		Totals: totals.stats(), Previous: previous.stats(), StackBy: query.Stack,
		Recent:  UsageRecent{Minutes: int(recentWindow / time.Minute), Requests: recent.requests, Tokens: recent.stats().TotalTokens},
		Buckets: make([]UsageBucket, len(starts)), Groups: map[string][]UsageGroup{}, Pairs: []UsagePair{},
	}
	for index, start := range starts {
		report.Buckets[index] = UsageBucket{At: time.UnixMilli(start).UTC(), UsageStats: buckets[index].stats()}
	}
	for _, dimension := range usageDimensions {
		report.Groups[dimension] = rankGroups(groups[dimension])
	}
	for pair, total := range pairs {
		report.Pairs = append(report.Pairs, UsagePair{Account: pair[0], Model: pair[1], UsageStats: total.stats()})
	}
	slices.SortFunc(report.Pairs, func(a, b UsagePair) int {
		return cmp.Or(cmp.Compare(b.Requests, a.Requests), strings.Compare(a.Account, b.Account), strings.Compare(a.Model, b.Model))
	})
	report.Series = stackSeries(report.Groups[query.Stack], stack, len(starts))
	var latest sql.NullInt64
	if err := store.db.QueryRowContext(ctx, `SELECT MAX(time) FROM requests`).Scan(&latest); err != nil {
		return UsageReport{}, err
	}
	if latest.Valid {
		at := time.UnixMilli(latest.Int64).UTC()
		report.LatestAt = &at
	}
	if clause, _ := filterSQL(query.Filters); clause != "" {
		options, err := store.options(ctx, segments)
		if err != nil {
			return UsageReport{}, err
		}
		report.Options = options
		return report, nil
	}
	report.Options = map[string][]string{}
	for _, dimension := range Dimensions {
		report.Options[dimension] = slices.Sorted(maps.Keys(groups[dimension]))
	}
	return report, nil
}

// rankGroups 按请求数降序、取值升序排列维度分项
func rankGroups(values map[string]*aggregate) []UsageGroup {
	groups := make([]UsageGroup, 0, len(values))
	for key, total := range values {
		groups = append(groups, UsageGroup{Key: key, UsageStats: total.stats()})
	}
	slices.SortFunc(groups, func(a, b UsageGroup) int {
		return cmp.Or(cmp.Compare(b.Requests, a.Requests), strings.Compare(a.Key, b.Key))
	})
	return groups
}

// stackSeries 取请求数最多的取值生成逐桶序列，其余取值合并为其他
func stackSeries(ranked []UsageGroup, values map[string][][2]int64, buckets int) []UsageSeries {
	series := []UsageSeries{}
	for index, group := range ranked {
		if index < stackLimit {
			series = append(series, UsageSeries{Key: group.Key, Requests: make([]int64, buckets), Tokens: make([]int64, buckets)})
		} else if index == stackLimit {
			series = append(series, UsageSeries{Other: true, Requests: make([]int64, buckets), Tokens: make([]int64, buckets)})
		}
		target := &series[len(series)-1]
		for bucket, value := range values[group.Key] {
			target.Requests[bucket] += value[0]
			target.Tokens[bucket] += value[1]
		}
	}
	return series
}

// options 返回各段内维度出现过的取值，带筛选的查询用它列出不受筛选影响的候选
func (store *Store) options(ctx context.Context, segments []segment) (map[string][]string, error) {
	seen := map[string]map[string]struct{}{}
	for _, dimension := range Dimensions {
		seen[dimension] = map[string]struct{}{}
	}
	values := make([]string, len(Dimensions))
	targets := make([]any, len(values))
	for index := range values {
		targets[index] = &values[index]
	}
	for _, part := range segments {
		table, column := part.source.table()
		rows, err := store.db.QueryContext(ctx, `SELECT DISTINCT `+strings.Join(Dimensions, ", ")+` FROM `+table+
			` WHERE `+column+` >= ? AND `+column+` < ?`, part.from, part.to)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			if err := rows.Scan(targets...); err != nil {
				return nil, errors.Join(err, rows.Close())
			}
			for index, dimension := range Dimensions {
				seen[dimension][values[index]] = struct{}{}
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	options := map[string][]string{}
	for dimension, values := range seen {
		options[dimension] = slices.Sorted(maps.Keys(values))
	}
	return options, nil
}
