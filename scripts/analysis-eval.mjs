#!/usr/bin/env node

import { readFile } from "node:fs/promises";

function argument(name, fallback) {
  const index = process.argv.indexOf(name);
  return index >= 0 ? process.argv[index + 1] : fallback;
}

function usage() {
	console.error("Uso: node scripts/analysis-eval.mjs --live --manifest casos.json --baseline prompt-a.txt --candidate prompt-b.txt --endpoint http://localhost:4000");
  process.exit(2);
}

const manifestPath = argument("--manifest");
const baselinePath = argument("--baseline");
const candidatePath = argument("--candidate");
const endpoint = argument("--endpoint", "http://localhost:4000").replace(/\/$/, "");
const model = argument("--model", "inspection-vision");
const apiKey = argument("--api-key", process.env.LLM_API_KEY ?? "");
const schemaPath = argument("--schema", "scripts/analysis-output-schema.json");
if (!process.argv.includes("--live") || !manifestPath || !baselinePath || !candidatePath) usage();

const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
const baseline = await readFile(baselinePath, "utf8");
const candidate = await readFile(candidatePath, "utf8");
const schema = JSON.parse(await readFile(schemaPath, "utf8"));

function expectedKey(finding) {
  return `${finding.changeType ?? ""}|${finding.category ?? ""}|${(finding.evidenceIds ?? []).slice().sort().join(",")}`;
}

function score(expected, actual) {
  const remaining = new Set(actual.map(expectedKey));
  let truePositive = 0;
  for (const finding of expected) {
    if (remaining.delete(expectedKey(finding))) truePositive++;
  }
  return { truePositive, expected: expected.length, actual: actual.length };
}

async function runCase(testCase, systemPrompt) {
  const content = [{ type: "text", text: testCase.userPrompt ?? "Analise as evidências fornecidas." }];
  for (const image of testCase.images) {
    content.push({ type: "text", text: `evidenceId=${image.evidenceId}; source=${image.source}${image.pairId ? `; pairId=${image.pairId}; position=${image.position}` : ""}` });
    content.push({ type: "image_url", image_url: { url: image.dataUrl, detail: "high" } });
  }
  const started = Date.now();
  const response = await fetch(`${endpoint}/v1/chat/completions`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}) },
    body: JSON.stringify({
      model,
      messages: [{ role: "system", content: systemPrompt }, { role: "user", content }],
      response_format: { type: "json_schema", json_schema: { name: "inspection_analysis", strict: true, schema } },
    }),
  });
  const body = await response.json();
  if (!response.ok) throw new Error(`${response.status}: ${JSON.stringify(body)}`);
  const parsed = JSON.parse(body.choices?.[0]?.message?.content ?? "{}");
  return { result: parsed, latencyMs: Date.now() - started, usage: body.usage ?? null };
}

const output = { manifest: manifest.name ?? manifestPath, cases: [], totals: { baseline: { truePositive: 0, expected: 0, actual: 0 }, candidate: { truePositive: 0, expected: 0, actual: 0 } } };
for (const testCase of manifest.cases ?? []) {
  const expected = testCase.expectedFindings ?? [];
  const row = { id: testCase.id, baseline: null, candidate: null };
  for (const [name, prompt] of [["baseline", baseline], ["candidate", candidate]]) {
    const run = await runCase(testCase, prompt);
    const metrics = score(expected, run.result.findings ?? []);
    row[name] = { coverageStatus: run.result.coverageStatus, comparisonStatus: run.result.comparisonStatus, metrics, latencyMs: run.latencyMs, usage: run.usage };
    for (const key of Object.keys(metrics)) output.totals[name][key] += metrics[key];
  }
  output.cases.push(row);
}
console.log(JSON.stringify(output, null, 2));
