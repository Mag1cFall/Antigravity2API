// origin 是客户端访问本服务的地址，取管理端页面自身的协议、主机与端口
export function serviceOrigin() {
  return window.location.origin
}

// Protocol 是公开 API 的协议
export type Protocol = 'openai' | 'anthropic' | 'gemini'

// endpoints 返回各协议 SDK 填写的 base URL 与主要接口路径
export function endpoints(origin: string): { protocol: Protocol; base: string; paths: string[] }[] {
  return [
    {
      protocol: 'openai',
      base: origin + '/v1',
      paths: ['/v1/chat/completions', '/v1/responses'],
    },
    { protocol: 'anthropic', base: origin, paths: ['/v1/messages', '/v1/messages/count_tokens'] },
    {
      protocol: 'gemini',
      base: origin,
      paths: ['/v1beta/models/{model}:generateContent'],
    },
  ]
}
