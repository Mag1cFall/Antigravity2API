import { Code, KeyRound, Link2 } from 'lucide-react'

import { Card, CardBody, CardHeader } from '@/components/ui/card'
import { PageHeader } from '@/components/ui/page'
import { AddKeyButton, ApiKeyList, useApiKeys } from '@/features/connect/ApiKeys'
import { ClientConfigs } from '@/features/connect/ClientConfigs'
import { EndpointList } from '@/features/connect/EndpointList'
import { connectMessages } from '@/features/connect/messages'
import { useMessages } from '@/lib/i18n'

// KeysCard 是 API key 的生成与管理
function KeysCard() {
  const m = useMessages(connectMessages)
  const { keys } = useApiKeys()
  return (
    <Card>
      <CardHeader
        icon={<KeyRound size={16} aria-hidden />}
        title={m.keys}
        description={m.keysHint}
        actions={keys.length > 0 && <AddKeyButton />}
      />
      <CardBody>
        <ApiKeyList />
      </CardBody>
    </Card>
  )
}

// ConnectPage 是接入页：三种协议的接口地址、API key 管理与各客户端的现成配置
export function ConnectPage() {
  const m = useMessages(connectMessages)
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader
            icon={<Link2 size={16} aria-hidden />}
            title={m.endpoints}
            description={m.endpointsHint}
          />
          <CardBody>
            <EndpointList />
          </CardBody>
        </Card>
        <KeysCard />
      </div>
      <Card>
        <CardHeader
          icon={<Code size={16} aria-hidden />}
          title={m.clients}
          description={m.clientsHint}
        />
        <CardBody>
          <ClientConfigs />
        </CardBody>
      </Card>
    </>
  )
}
