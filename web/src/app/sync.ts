import { hashKey, type QueryClient, type QueryKey } from '@tanstack/react-query'

import { accountsKey } from '@/features/accounts/queries'
import type { Account } from '@/features/accounts/types'

// accountDependents 是账户的就绪状态或额度读取时间变化后需要重新读取的查询：模型目录与各模型的可用账户数、额度变化曲线
const accountDependents: QueryKey[] = [['models'], ['quota-snapshots']]

// requestDependents 是请求结束后需要重新读取的查询：请求记录、最近请求与用量
export const requestDependents: QueryKey[] = [['requests'], ['usage']]

// settingsDependents 是保存设置后需要重新读取的查询：模型目录中的模型映射、PROXY 组成的代理池
export const settingsDependents: QueryKey[] = [['models'], ['proxies']]

// invalidate 让 keys 下的查询重新读取，正在读取的查询不打断，读完后再读一次
export function invalidate(client: QueryClient, keys: QueryKey[]) {
  for (const queryKey of keys) {
    const fetching = client.isFetching({ queryKey }) > 0
    void client.invalidateQueries({ queryKey }, { cancelRefetch: false }).then(() => {
      if (fetching) void client.invalidateQueries({ queryKey }, { cancelRefetch: false })
    })
  }
}

// accountsHash 是账户列表查询键的哈希
const accountsHash = hashKey(accountsKey)

// catalogState 是账户列表中决定模型目录的部分：各账户的 ID、状态与额度读取时间
function catalogState(accounts: Account[]) {
  return accounts
    .map((account) => [account.id, account.state, account.quota_at].join(' '))
    .join('\n')
}

// syncQueries 在账户列表缓存的每次写入（事件流、操作结果与重新读取）后，模型目录相关部分变化时让依赖它的查询重新读取
export function syncQueries(client: QueryClient) {
  let last: string | undefined
  return client.getQueryCache().subscribe((event) => {
    if (event.type !== 'updated' || event.action.type !== 'success') return
    if (event.query.queryHash !== accountsHash) return
    const data = event.query.state.data as { accounts: Account[] } | undefined
    if (!data) return
    const next = catalogState(data.accounts)
    if (last !== undefined && next !== last) invalidate(client, accountDependents)
    last = next
  })
}
