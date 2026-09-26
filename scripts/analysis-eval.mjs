#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { createHash } from "node:crypto";

function argument(name, fallback) {
  const index = process.argv.indexOf(name);
  return index >= 0 ? process.argv[index + 1] : fallback;
}

function usage() {
  console.error("Uso: node scripts/analysis-eval.mjs --live --manifest casos.json --baseline prompt-a.txt --candidate prompt-b.txt [--repetitions 3] [--model inspection-vision] [--cache-mode implicit|explicit|both] [--endpoint http://localhost:4000]");
  process.exit(2);
}

const manifestPath = argument("--manifest");
const baselinePath = argument("--baseline");
const candidatePath = argument("--candidate");
const endpoint = argument("--endpoint", "http://localhost:4000").replace(/\/$/, "");
const model = argument("--model", "inspection-vision");
const baselineModel = argument("--baseline-model", model);
const candidateModel = argument("--candidate-model", model);
const repetitions = Number(argument("--repetitions", "3"));
const cacheMode = argument("--cache-mode", "implicit");
const minimumCacheTokensOverride = Number(argument("--minimum-cache-tokens", "0"));
const cacheReadRate = Number(argument("--cache-read-usd-per-million-tokens", "NaN"));
const cacheWriteRate = Number(argument("--cache-write-usd-per-million-tokens", "NaN"));
const cacheStorageRate = Number(argument("--cache-storage-usd-per-million-token-seconds", "NaN"));
const requiredScenarios = ["conservation", "inventory_difference", "dirt", "obstruction", "insufficient_quality", "no_change", "thin_crack", "small_stain", "lighting_variation"];
const apiKey = argument("--api-key", process.env.LLM_API_KEY ?? "");
const schemaPath = argument("--schema", "scripts/analysis-output-schema.json");
if (!process.argv.includes("--live") || !manifestPath || !baselinePath || !candidatePath || !Number.isInteger(repetitions) || repetitions < 1 || repetitions > 20 || !["implicit", "explicit", "both"].includes(cacheMode)) usage();

const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
const baseline = await readFile(baselinePath, "utf8");
const candidate = await readFile(candidatePath, "utf8");
const schema = JSON.parse(await readFile(schemaPath, "utf8"));

function findingKey(finding) {
  return `${finding.category ?? ""}|${(finding.evidenceIds ?? []).slice().sort().join(",")}`;
}

function score(expected, actual, expectedNoRelevantChange, actualNoRelevantChange) {
  const remaining = new Set(actual.map(findingKey));
  let truePositive = 0;
  for (const finding of expected) if (remaining.delete(findingKey(finding))) truePositive++;
  return {
    truePositive,
    expected: expected.length,
    actual: actual.length,
    precision: actual.length ? truePositive / actual.length : (expected.length === 0 ? 1 : 0),
    recall: expected.length ? truePositive / expected.length : (actual.length === 0 ? 1 : 0),
    highCriticalExpected: expected.filter((finding) => ["HIGH", "CRITICAL"].includes(String(finding.severity ?? "").toUpperCase())).length,
    highCriticalMissed: expected.filter((finding) => ["HIGH", "CRITICAL"].includes(String(finding.severity ?? "").toUpperCase()) && !actual.some((candidate) => findingKey(candidate) === findingKey(finding))).length,
    noRelevantChangeCorrect: expectedNoRelevantChange === undefined ? null : expectedNoRelevantChange === actualNoRelevantChange,
  };
}

function imageBytes(dataUrl) {
  const encoded = dataUrl.slice(dataUrl.indexOf(",") + 1).replace(/\s/g, "");
  return Math.floor(encoded.length * 3 / 4) - (encoded.endsWith("==") ? 2 : encoded.endsWith("=") ? 1 : 0);
}

function photoSignature(images) {
  return createHash("sha256").update(images.map((image) => image.dataUrl ?? "").join("\n")).digest("hex");
}

function promptDigest(prompt) {
  return createHash("sha256").update(prompt).digest("hex");
}

function minimumExplicitCacheTokens(modelName) {
  if (minimumCacheTokensOverride > 0) return minimumCacheTokensOverride;
  if (/gemini-(2\.5|2-5)/i.test(modelName)) return 2048;
  if (/gemini-(3|3\.)/i.test(modelName)) return 4096;
  return null;
}

async function providerTokenCount(modelName, prompt) {
  const response = await fetch(`${endpoint}/utils/token_counter?call_endpoint=true`, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}) },
    body: JSON.stringify({ model: modelName, prompt }),
  });
  const body = await response.json();
  if (!response.ok || !Number.isInteger(body.total_tokens)) throw new Error(`tokenizador do provedor indisponível para ${modelName}: ${JSON.stringify(body)}`);
  return { tokens: body.total_tokens, tokenizer: body.tokenizer_type ?? "provider", modelUsed: body.model_used ?? null };
}

async function runCase(testCase, systemPrompt, selectedModel, selectedCacheMode, explicitCacheEligible, images) {
  if (typeof testCase.requirement !== "string" || typeof testCase.confidenceThreshold !== "number" || testCase.confidenceThreshold < 0 || testCase.confidenceThreshold > 1 || !Object.hasOwn(testCase, "expectedNoRelevantChange") || ![true, false, null].includes(testCase.expectedNoRelevantChange)) {
    throw new Error(`${testCase.id}: requirement, confidenceThreshold and expectedNoRelevantChange are required`);
  }
  if (!Array.isArray(images) || images.length === 0) throw new Error(`${testCase.id}: each image configuration needs at least one image`);
  const content = [{ type: "text", text: `requirement: ${testCase.requirement}\nconfidenceThreshold: ${testCase.confidenceThreshold}` }];
  let imagePayloadBytes = 0;
  let imagePayloadBytesAbove2048 = 0;
  let knownHighResolutionImages = 0;
  for (const image of images) {
    if (!image.evidenceId || !["ORIGIN", "CURRENT"].includes(image.role) || !image.dataUrl) throw new Error(`${testCase.id}: every image requires evidenceId, role and dataUrl`);
    const bytes = imageBytes(image.dataUrl);
    imagePayloadBytes += bytes;
    const sourceWidth = image.sourceWidth ?? image.width;
    const sourceHeight = image.sourceHeight ?? image.height;
    if (Number.isFinite(sourceWidth) && Number.isFinite(sourceHeight)) {
      if (Math.max(sourceWidth, sourceHeight) > 2048) {
        imagePayloadBytesAbove2048 += bytes;
        knownHighResolutionImages++;
      }
    }
    content.push({ type: "text", text: `evidenceId=${image.evidenceId}; role=${image.role}${image.pairId ? `; pairId=${image.pairId}; position=${image.position}` : ""}` });
    content.push({ type: "image_url", image_url: { url: image.dataUrl, detail: "high" } });
  }
  if (selectedCacheMode === "explicit" && !explicitCacheEligible) return { skipped: "prompt fixo abaixo do mínimo documentado do modelo", imagePayloadBytes, imagePayloadBytesAbove2048, knownHighResolutionImages };
  const request = {
    model: selectedModel,
    messages: [selectedCacheMode === "explicit"
      ? { role: "system", content: [{ type: "text", text: systemPrompt, cache_control: { type: "ephemeral", ttl: "600s" } }] }
      : { role: "system", content: systemPrompt },
    { role: "user", content }],
    response_format: { type: "json_schema", json_schema: { name: "inspection_analysis", strict: true, schema } },
  };
  const requestBody = JSON.stringify(request);
  const started = Date.now();
  try {
    const response = await fetch(`${endpoint}/v1/chat/completions`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...(apiKey ? { Authorization: `Bearer ${apiKey}` } : {}) },
      body: requestBody,
    });
    const body = await response.json();
    const latencyMs = Date.now() - started;
    if (!response.ok) return { error: `${response.status}: ${JSON.stringify(body)}`, latencyMs, usage: body.usage ?? null, requestBodyBytes: Buffer.byteLength(requestBody), imagePayloadBytes, imagePayloadBytesAbove2048, knownHighResolutionImages };
    try {
      const result = JSON.parse(body.choices?.[0]?.message?.content ?? "{}");
      if (!Array.isArray(result.findings) || typeof result.noRelevantChange !== "boolean") throw new Error("resposta não atende o schema esperado");
      return { result, latencyMs, usage: body.usage ?? null, reportedCost: body.cost ?? null, requestBodyBytes: Buffer.byteLength(requestBody), imagePayloadBytes, imagePayloadBytesAbove2048, knownHighResolutionImages };
    } catch (error) {
      return { error: `resposta inválida: ${error.message}`, latencyMs, usage: body.usage ?? null, reportedCost: body.cost ?? null, requestBodyBytes: Buffer.byteLength(requestBody), imagePayloadBytes, imagePayloadBytesAbove2048, knownHighResolutionImages };
    }
  } catch (error) {
    return { error: error.message, latencyMs: Date.now() - started, usage: null, requestBodyBytes: Buffer.byteLength(requestBody), imagePayloadBytes, imagePayloadBytesAbove2048, knownHighResolutionImages };
  }
}

function percentile(values, fraction) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.min(sorted.length - 1, Math.ceil(fraction * sorted.length) - 1)];
}

function summarize(runs) {
  const successful = runs.filter((run) => run.metrics);
  const latencies = runs.map((run) => run.latencyMs).filter(Number.isFinite);
  const numeric = (key) => runs.map((run) => Number(run.usage?.[key])).filter(Number.isFinite);
  const cached = runs.map((run) => run.usage?.prompt_tokens_details?.cached_tokens ?? run.usage?.cache_read_input_tokens).filter((value) => Number.isFinite(value));
  const cacheCreation = runs.map((run) => run.usage?.cache_creation_input_tokens ?? run.usage?.cache_creation_tokens).filter((value) => Number.isFinite(value));
  const sum = (values) => values.reduce((total, value) => total + value, 0);
  return {
    repetitions: runs.length,
    invalidResponses: runs.filter((run) => run.error).length,
    latencyMedianMs: percentile(latencies, 0.5),
    latencyP95Ms: percentile(latencies, 0.95),
    imagePayloadBytes: runs[0]?.imagePayloadBytes ?? 0,
    imagePayloadBytesAbove2048: runs[0]?.imagePayloadBytesAbove2048 ?? 0,
    knownHighResolutionImages: runs[0]?.knownHighResolutionImages ?? 0,
    requestBodyBytes: runs[0]?.requestBodyBytes ?? 0,
    inputTokens: sum(numeric("prompt_tokens")),
    outputTokens: sum(numeric("completion_tokens")),
    cachedInputTokens: cached.length ? sum(cached) : null,
    cacheCreationInputTokens: cacheCreation.length ? sum(cacheCreation) : null,
    cacheKnownCalls: cached.length,
    cacheHitCalls: cached.filter((value) => value > 0).length,
    distinctPhotoCalls: runs.filter((run) => run.cachePhotoScenario === "distinct-photo").length,
    identicalPhotoRepeatCalls: runs.filter((run) => run.cachePhotoScenario === "identical-photo-repeat").length,
    distinctPhotoCacheHitCalls: runs.filter((run) => run.cachePhotoScenario === "distinct-photo" && Number(run.usage?.prompt_tokens_details?.cached_tokens ?? run.usage?.cache_read_input_tokens) > 0).length,
    reportedCost: sum(runs.map((run) => Number(run.reportedCost ?? run.usage?.cost)).filter(Number.isFinite)),
    scores: {
      truePositive: sum(successful.map((run) => run.metrics.truePositive)),
      expected: sum(successful.map((run) => run.metrics.expected)),
      actual: sum(successful.map((run) => run.metrics.actual)),
      highCriticalExpected: sum(successful.map((run) => run.metrics.highCriticalExpected)),
      highCriticalMissed: sum(successful.map((run) => run.metrics.highCriticalMissed)),
      precision: successful.length ? successful.reduce((total, run) => total + run.metrics.precision, 0) / successful.length : null,
      recall: successful.length ? successful.reduce((total, run) => total + run.metrics.recall, 0) / successful.length : null,
      noRelevantChangeScored: successful.filter((run) => run.metrics.noRelevantChangeCorrect !== null).length,
      noRelevantChangeCorrect: successful.filter((run) => run.metrics.noRelevantChangeCorrect === true).length,
    },
  };
}

const output = {
  manifest: manifest.name ?? manifestPath,
  model,
  baselineModel,
  candidateModel,
  cacheMode,
  cacheModes: cacheMode === "both" ? ["implicit", "explicit"] : [cacheMode],
  repetitions,
  explicitCache: null,
  requiredScenarios,
  cases: [],
  totals: { baseline: [], candidate: [] },
};
const seenPhotos = { baseline: { implicit: new Set(), explicit: new Set() }, candidate: { implicit: new Set(), explicit: new Set() } };
const promptMeasurements = await Promise.allSettled([
  providerTokenCount(baselineModel, baseline),
  providerTokenCount(candidateModel, candidate),
]);
  output.promptTokenCounts = {
  baseline: { promptDigest: promptDigest(baseline), ...(promptMeasurements[0].status === "fulfilled" ? promptMeasurements[0].value : { unavailable: promptMeasurements[0].reason?.message ?? "tokenizador indisponível" }) },
  candidate: { promptDigest: promptDigest(candidate), ...(promptMeasurements[1].status === "fulfilled" ? promptMeasurements[1].value : { unavailable: promptMeasurements[1].reason?.message ?? "tokenizador indisponível" }) },
};
if (output.cacheModes.includes("explicit")) {
  if (promptMeasurements.some((result) => result.status !== "fulfilled")) throw new Error("Cache explícito exige contagem de tokens bem-sucedida no tokenizador do provedor.");
  const counts = promptMeasurements.map((result) => result.value);
  const baselineMinimum = minimumExplicitCacheTokens(counts[0].modelUsed ?? baselineModel);
  const candidateMinimum = minimumExplicitCacheTokens(counts[1].modelUsed ?? candidateModel);
  output.explicitCache = {
    ttlSeconds: 600,
    baseline: { promptDigest: promptDigest(baseline), model: baselineModel, ...counts[0], minimumTokens: baselineMinimum, eligible: baselineMinimum !== null && counts[0].tokens >= baselineMinimum },
    candidate: { promptDigest: promptDigest(candidate), model: candidateModel, ...counts[1], minimumTokens: candidateMinimum, eligible: candidateMinimum !== null && counts[1].tokens >= candidateMinimum },
    note: "usage.cost contém apenas o valor informado pelo LiteLLM; tokens de criação e leituras são reportados separadamente quando disponíveis.",
  };
}
for (const testCase of manifest.cases ?? []) {
  const expected = testCase.expectedFindings ?? [];
  const row = { id: testCase.id, scenarios: testCase.scenarios ?? [], requirement: testCase.requirement, confidenceThreshold: testCase.confidenceThreshold, expectedNoRelevantChange: testCase.expectedNoRelevantChange, expectedFindings: expected, baseline: [], candidate: [] };
  for (let repetition = 0; repetition < repetitions; repetition++) {
    const order = repetition % 2 === 0 ? ["baseline", "candidate"] : ["candidate", "baseline"];
    for (const name of order) {
      const prompt = name === "baseline" ? baseline : candidate;
      const selectedModel = name === "baseline" ? baselineModel : candidateModel;
      const images = testCase[`${name}Images`] ?? testCase.images ?? [];
      for (const selectedCacheMode of output.cacheModes) {
      const eligible = name === "baseline" ? output.explicitCache?.baseline.eligible : output.explicitCache?.candidate.eligible;
      const run = await runCase(testCase, prompt, selectedModel, selectedCacheMode, eligible, images);
      if (run.skipped) {
        row[name].push({ repetition: repetition + 1, cacheMode: selectedCacheMode, skipped: run.skipped });
        continue;
      }
      const metrics = run.result ? score(expected, run.result.findings, testCase.expectedNoRelevantChange, run.result.noRelevantChange) : null;
      const signature = `${selectedModel}|${promptDigest(prompt)}|${photoSignature(images)}`;
      const cachePhotoScenario = seenPhotos[name][selectedCacheMode].has(signature) ? "identical-photo-repeat" : "distinct-photo";
      seenPhotos[name][selectedCacheMode].add(signature);
      row[name].push({ repetition: repetition + 1, cacheMode: selectedCacheMode, noRelevantChange: run.result?.noRelevantChange ?? null, metrics, cachePhotoScenario, ...run });
      }
    }
  }
  row.summary = { baseline: {}, candidate: {} };
  for (const name of ["baseline", "candidate"]) {
    for (const mode of output.cacheModes) {
      row.summary[name][mode] = summarize(row[name].filter((run) => run.cacheMode === mode));
      output.totals[name].push({ id: row.id, cacheMode: mode, ...row.summary[name][mode] });
    }
  }
  output.cases.push(row);
}
const observedScenarios = new Set(output.cases.flatMap((row) => row.scenarios));
output.sampleCoverage = {
  covered: requiredScenarios.filter((scenario) => observedScenarios.has(scenario)),
  missing: requiredScenarios.filter((scenario) => !observedScenarios.has(scenario)),
  unknown: [...observedScenarios].filter((scenario) => !requiredScenarios.includes(scenario)),
};
const aggregate = (name, mode) => {
  const summaries = output.totals[name].filter((item) => item.cacheMode === mode);
  const sum = (key) => summaries.reduce((total, item) => total + (Number(item.scores[key]) || 0), 0);
  const tp = sum("truePositive");
  const expected = sum("expected");
  const actual = sum("actual");
  const noRelevantScored = sum("noRelevantChangeScored");
  const noRelevantCorrect = sum("noRelevantChangeCorrect");
  return {
    precision: actual ? tp / actual : expected === 0 ? 1 : 0,
    recall: expected ? tp / expected : actual === 0 ? 1 : 0,
    noRelevantChangeAccuracy: noRelevantScored ? noRelevantCorrect / noRelevantScored : null,
    highCriticalExpected: sum("highCriticalExpected"),
    highCriticalMissed: sum("highCriticalMissed"),
    imagePayloadBytesAbove2048: summaries.reduce((total, item) => total + item.imagePayloadBytesAbove2048, 0),
    knownHighResolutionImages: summaries.reduce((total, item) => total + item.knownHighResolutionImages, 0),
  };
};
output.qualityGate = {};
for (const mode of output.cacheModes) {
  const baselineMetrics = aggregate("baseline", mode);
  const candidateMetrics = aggregate("candidate", mode);
  const highResolutionReduction = baselineMetrics.imagePayloadBytesAbove2048 > 0
    ? 1 - candidateMetrics.imagePayloadBytesAbove2048 / baselineMetrics.imagePayloadBytesAbove2048
    : null;
  const qualityDrops = {
    precision: baselineMetrics.precision - candidateMetrics.precision,
    recall: baselineMetrics.recall - candidateMetrics.recall,
    noRelevantChangeAccuracy: baselineMetrics.noRelevantChangeAccuracy === null || candidateMetrics.noRelevantChangeAccuracy === null
      ? null : baselineMetrics.noRelevantChangeAccuracy - candidateMetrics.noRelevantChangeAccuracy,
  };
  const reasons = [];
  if (output.sampleCoverage.missing.length) reasons.push("mandatory sample scenarios are missing");
  if (promptDigest(baseline) !== promptDigest(candidate)) reasons.push("baseline and candidate prompts differ; image-only quality comparison is confounded");
  if (baselineModel !== candidateModel) reasons.push("baseline and candidate models differ; image-only quality comparison is confounded");
  if (candidateMetrics.highCriticalMissed > baselineMetrics.highCriticalMissed) reasons.push("candidate adds HIGH or CRITICAL misses");
  for (const [name, drop] of Object.entries(qualityDrops)) if (drop !== null && drop > 0.02) reasons.push(`${name} drops by more than two percentage points`);
  if (baselineMetrics.knownHighResolutionImages === 0) reasons.push("high-resolution source dimensions are missing");
  if (highResolutionReduction === null || highResolutionReduction < 0.30) reasons.push("high-resolution image payload reduction is below 30% or cannot be measured");
  output.qualityGate[mode] = { passed: reasons.length === 0, reasons, baseline: baselineMetrics, candidate: candidateMetrics, qualityDrops, highResolutionReduction };
}
if (output.cacheModes.includes("explicit")) {
  const allRuns = output.cases.flatMap((row) => [...row.baseline, ...row.candidate]);
  const explicitRuns = allRuns.filter((run) => run.cacheMode === "explicit");
  const implicitRuns = allRuns.filter((run) => run.cacheMode === "implicit");
  const sumUsage = (runs, extract) => runs.reduce((sum, run) => sum + (Number(extract(run)) || 0), 0);
  const cacheReadTokens = sumUsage(explicitRuns, (run) => run.usage?.prompt_tokens_details?.cached_tokens ?? run.usage?.cache_read_input_tokens);
  const cacheWriteTokens = sumUsage(explicitRuns, (run) => run.usage?.cache_creation_input_tokens ?? run.usage?.cache_creation_tokens);
  const explicitReportedCost = sumUsage(explicitRuns, (run) => run.reportedCost ?? run.usage?.cost);
  const implicitReportedCost = sumUsage(implicitRuns, (run) => run.reportedCost ?? run.usage?.cost);
  const estimatedTokenSeconds = cacheWriteTokens * 600;
  const ratesAvailable = [cacheReadRate, cacheWriteRate, cacheStorageRate].every(Number.isFinite);
  output.explicitCache.economics = {
    cacheReadTokens,
    cacheWriteTokens,
    implicitReportedCost: implicitRuns.length ? implicitReportedCost : null,
    explicitReportedCost: explicitRuns.length ? explicitReportedCost : null,
    cacheStorageTokenSecondsUpperBound: estimatedTokenSeconds,
    suppliedRates: ratesAvailable ? { cacheReadRate, cacheWriteRate, cacheStorageRate } : null,
    estimatedCacheChargesUsd: ratesAvailable ? (cacheReadTokens * cacheReadRate + cacheWriteTokens * cacheWriteRate) / 1_000_000 + estimatedTokenSeconds * cacheStorageRate / 1_000_000 : null,
    note: ratesAvailable ? "Custo de armazenamento usa o limite superior: TTL completo de 600 s para cada token gravado." : "Informe as três tarifas por CLI para estimar leitura, gravação e armazenamento; o custo informado pelo gateway continua disponível por configuração.",
  };
}
console.log(JSON.stringify(output, null, 2));
