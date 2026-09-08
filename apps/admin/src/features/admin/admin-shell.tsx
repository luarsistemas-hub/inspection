"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { beginPKCE } from "@/auth/pkce";
import { hasAdminAccess, setIdentity } from "@/auth/session";
import { graphql, type GraphQLFailure } from "@/graphql/client";

const links = [["Organização", "/organization"], ["Acessos", "/access"], ["Catálogos", "/catalogs"], ["Ativos", "/assets"], ["Governança", "/governance"], ["Auditoria", "/audit"]] as const;
const emptyID = "00000000-0000-0000-0000-000000000000";
type Me = { me: { tenantId: string; roles: string[]; productEntitlements: string[] }; tenant: { id: string; name: string; status: string } | null };
type BootstrapTenant = { createTenant: { tenant: { id: string; name: string; status: string } | null; userErrors: Array<{ message: string }> } };
const gateQuery = "query AdminGate { me { tenantId roles productEntitlements } tenant { id name status } }";

export function AdminShell({ section }: { section: string }) {
  const pathname = usePathname(); const router = useRouter();
  const [status, setStatus] = useState("Verificando acesso…"); const [allowed, setAllowed] = useState(false);
  const [needsBootstrap, setNeedsBootstrap] = useState(false); const [bootstrapBusy, setBootstrapBusy] = useState(false);
  const applyGate = useCallback(({ me, tenant }: Me) => {
    if (!tenant && me.tenantId === emptyID) { setAllowed(false); setNeedsBootstrap(true); setStatus("Primeiro acesso: crie a operação local para continuar."); return; }
    setNeedsBootstrap(false);
    const identity = { tenantId: me.tenantId, tenantName: tenant?.name ?? "Tenant", roles: me.roles, entitlements: me.productEntitlements };
    setIdentity(identity);
    if (tenant?.status !== "ACTIVE" || !hasAdminAccess(identity)) { setAllowed(false); setStatus("Acesso administrativo não autorizado. Nenhuma configuração foi carregada."); return; }
    setAllowed(true); setStatus(tenant.name);
  }, []);
  useEffect(() => {
    void graphql<Me>(gateQuery).then(applyGate).catch((error: GraphQLFailure) => setStatus(error.code === "FORBIDDEN" || error.code === "UNAUTHENTICATED" ? "Acesso administrativo não autorizado. Nenhuma configuração foi carregada." : error.message));
  }, [applyGate]);
  const bootstrap = async () => {
    setBootstrapBusy(true); setStatus("Criando operação local…");
    try {
      const { createTenant } = await graphql<BootstrapTenant>("mutation BootstrapTenant($input: CreateTenantInput!) { createTenant(input: $input) { tenant { id name status } userErrors { message } } }", { input: { name: "Minha operação", businessUnitCode: "MATRIZ", businessUnitName: "Matriz", clientMutationId: "local-bootstrap-admin" } });
      const error = createTenant.userErrors[0];
      if (error) throw new Error(error.message);
      applyGate(await graphql<Me>(gateQuery));
      setBootstrapBusy(false);
    } catch (error) { setBootstrapBusy(false); setStatus(error instanceof Error ? error.message : "Não foi possível criar a operação local."); }
  };
  const signIn = () => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", pathname);
  if (needsBootstrap) return <main className="denial"><h1>Primeiro acesso</h1><p role="status">{status}</p><button disabled={bootstrapBusy} onClick={() => void bootstrap()}>{bootstrapBusy ? "Criando…" : "Criar operação local"}</button></main>;
  if (!allowed) return <main className="denial"><h1>Administração</h1><p role="status">{status}</p><button onClick={signIn}>Entrar com conta administrativa</button><a href={process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}>Ir para o Dashboard</a></main>;
  return <main><header><strong>Inspeção · Super Admin</strong><span>{status}</span><a href={`${process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}/`}>Abrir Dashboard</a></header><nav aria-label="Administração">{links.map(([label, href]) => <Link key={href} aria-current={pathname === href ? "page" : undefined} href={href}>{label}</Link>)}</nav><section><h1>{section}</h1><button className="secondary" onClick={() => router.refresh()}>Atualizar</button><AdminContent section={section} /></section></main>;
}

function AdminContent({ section }: { section: string }) {
  const [query, setQuery] = useState(""); const [notice, setNotice] = useState("Selecione uma ação para consultar dados administrativos.");
  const action = section === "Organização" ? "businessUnits" : section === "Acessos" ? "memberships" : section === "Catálogos" ? "participants" : section === "Ativos" ? "assets" : section === "Governança" ? "publicationPolicy" : "auditEvents";
  const load = async () => {
    try {
      const isPolicy = action === "publicationPolicy";
      const operation = isPolicy ? `query Admin${action} { ${action} { mode version } }` : `query Admin${action}($first:Int!,$after:String,$search:String){ ${action}(first:$first,after:$after${["participants", "assets"].includes(action) ? ",search:$search" : ""}) { nodes { id } pageInfo { hasNextPage endCursor } } }`;
      const variables = isPolicy ? undefined : { first: 25, after: null, search: query || null };
      const result = await graphql<Record<string, unknown>>(operation, variables);
      setNotice(`${isPolicy ? "Consulta concluída" : "Consulta paginada concluída"}: ${JSON.stringify(result).slice(0, 100)}…`);
    } catch (error) {
      const failure = error as GraphQLFailure;
      setNotice(failure.code ? `${failure.message} (${failure.code})` : failure.message);
    }
  };
  return <div className="feature"><p>Use filtros e paginação para manter o escopo do tenant. Alterações exigem versão atual, confirmação e um identificador idempotente.</p><label>Filtrar <input value={query} onChange={(event) => setQuery(event.target.value)} /></label><button onClick={() => void load()}>Consultar</button><p role="status">{notice}</p><details><summary>Alterações e segurança</summary><p>Formulários preservam entradas não salvas, exibem conflito de versão para recarregar valores atuais e só confirmam uma mutação após resposta do servidor. Recursos arquivados permanecem históricos e não podem ser escolhidos para novas atribuições.</p></details></div>;
}
