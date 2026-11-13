import {
  client,
  methods,
  type Client,
  type ClientConnection,
  type Stream,
} from '@agentclientprotocol/sdk';
import {
  GOOSE_EXT_AGENT_REQUESTS,
  GOOSE_EXT_NOTIFICATIONS,
  GooseExtClient,
  type GooseSessionNotification_unstable,
  type ProviderDeviceCodeNotification_unstable,
  type RecipeParamsResponse_unstable,
  type RequestRecipeParams_unstable,
  zGooseSessionNotification_unstable,
  zProviderDeviceCodeNotification_unstable,
  zRequestRecipeParams_unstable,
} from '@aaif/goose-acp-client';

const [gooseSessionUpdate, providerDeviceCode] = GOOSE_EXT_NOTIFICATIONS;
const [gooseRecipeParamsRequest] = GOOSE_EXT_AGENT_REQUESTS;

export type GooseAcpCallbacks = Required<
  Pick<Client, 'requestPermission' | 'sessionUpdate' | 'createElicitation'>
> & {
  unstable_sessionRecipeRequestParams: (
    request: RequestRecipeParams_unstable
  ) => Promise<RecipeParamsResponse_unstable>;
  unstable_sessionUpdate: (notification: GooseSessionNotification_unstable) => Promise<void>;
  unstable_providerDeviceCode: (
    notification: ProviderDeviceCodeNotification_unstable
  ) => Promise<void>;
};

export type GooseAcpClient = {
  connection: ClientConnection;
  goose: GooseExtClient;
};

export function connectGooseAcpClient(
  stream: Stream,
  callbacks: GooseAcpCallbacks
): GooseAcpClient {
  const app = client({ name: 'goose' })
    .onRequest(methods.client.session.requestPermission, (context) =>
      callbacks.requestPermission(context.params)
    )
    .onNotification(methods.client.session.update, (context) =>
      callbacks.sessionUpdate(context.params)
    )
    .onRequest(methods.client.elicitation.create, (context) =>
      callbacks.createElicitation(context.params)
    )
    .onRequest(gooseRecipeParamsRequest.method, zRequestRecipeParams_unstable, (context) =>
      callbacks.unstable_sessionRecipeRequestParams(context.params)
    )
    .onNotification(gooseSessionUpdate.method, zGooseSessionNotification_unstable, (context) =>
      callbacks.unstable_sessionUpdate(context.params)
    )
    .onNotification(
      providerDeviceCode.method,
      zProviderDeviceCodeNotification_unstable,
      (context) => callbacks.unstable_providerDeviceCode(context.params)
    );

  const connection = app.connect(stream);
  // Coalesce identical in-flight read requests: multiple components fetch the
  // same metadata on the same tick (extensions/list x6 per session click,
  // providers/list x2, ...), and each duplicate costs a full round trip on
  // the click-to-load critical path. One wire request serves every caller.
  installReadRequestCoalescer(connection);
  const goose = new GooseExtClient(connection.agent);

  return { connection, goose };
}

const COALESCED_READ_METHODS: ReadonlySet<string> = new Set<string>([
  '_goose/unstable/session/extensions/list',
  '_goose/unstable/session/info',
  '_goose/unstable/providers/list',
  '_goose/unstable/providers/canonical-model-info',
  '_goose/unstable/config/read',
  '_goose/unstable/dictation/config',
  '_goose/unstable/session/live-voice/availability',
  methods.agent.session.list,
]);

function installReadRequestCoalescer(connection: ClientConnection): void {
  const agent = connection.agent as unknown as {
    request: (method: string, params?: unknown, options?: unknown) => Promise<unknown>;
  };
  const original = agent.request.bind(connection.agent);
  const inflight = new Map<string, Promise<unknown>>();

  agent.request = (method, params, options) => {
    if (!COALESCED_READ_METHODS.has(method)) {
      return original(method, params, options);
    }
    let key: string;
    try {
      key = `${method} ${JSON.stringify(params)}`;
    } catch {
      return original(method, params, options);
    }
    const existing = inflight.get(key);
    if (existing) {
      return existing;
    }
    const pending = Promise.resolve(original(method, params, options)).finally(() => {
      inflight.delete(key);
    });
    inflight.set(key, pending);
    return pending;
  };
}
