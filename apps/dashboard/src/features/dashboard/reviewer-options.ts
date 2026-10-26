import { graphql } from "@/graphql/client";
import { DashboardReviewersDocument, type DashboardReviewersQuery, type DashboardReviewersQueryVariables } from "@/graphql/generated";

export async function loadDashboardReviewers() {
  const memberships: DashboardReviewersQuery["memberships"]["nodes"] = [];
  let after: string | null = null;
  let hasNextPage = true;
  while (hasNextPage) {
    const result: DashboardReviewersQuery = await graphql<DashboardReviewersQuery, DashboardReviewersQueryVariables>(DashboardReviewersDocument, { first: 100, after });
    memberships.push(...result.memberships.nodes);
    after = result.memberships.pageInfo.endCursor ?? null;
    hasNextPage = result.memberships.pageInfo.hasNextPage;
    if (hasNextPage && !after) throw new Error("A lista de revisores não informou o próximo cursor.");
  }
  return memberships.filter((membership) => membership.status === "ACTIVE" && ["EMPLOYEE", "MANAGER", "TENANT_ADMIN"].includes(membership.role));
}
