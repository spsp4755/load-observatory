const MAX_TRACE_EVENTS = 10000;

function timestampMillis(value) {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") {
    const numeric = Number(value);
    if (Number.isFinite(numeric)) return numeric;
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  throw new Error("각 항목에 timestamp_ms 또는 timestamp가 필요합니다.");
}

export function parseTraceReplay(text) {
  const source = text.trim();
  if (!source) throw new Error("트레이스 파일이 비어 있습니다.");
  let rows;
  try {
    const parsed = JSON.parse(source);
    rows = Array.isArray(parsed) ? parsed : parsed.events;
  } catch {
    rows = source.split(/\r?\n/).filter(Boolean).map((line, index) => {
      try {
        return JSON.parse(line);
      } catch {
        throw new Error(`JSONL ${index + 1}번째 줄을 읽을 수 없습니다.`);
      }
    });
  }
  if (!Array.isArray(rows) || rows.length === 0) throw new Error("트레이스 이벤트가 없습니다.");
  if (rows.length > MAX_TRACE_EVENTS) throw new Error(`트레이스는 최대 ${MAX_TRACE_EVENTS.toLocaleString()}건까지 지원합니다.`);

  const times = rows.map((row) => timestampMillis(row.timestamp_ms ?? row.timestamp ?? row.time_ms ?? row.at_ms));
  for (let index = 1; index < times.length; index += 1) {
    if (times[index] < times[index - 1]) throw new Error("트레이스는 시간순으로 정렬되어야 합니다.");
  }
  const first = times[0];
  return rows.map((row, index) => ({
    timestamp_ms: Math.round(times[index] - first),
    name: String(row.name ?? row.scenario ?? "trace"),
    prompt: String(row.prompt ?? row.input ?? ""),
    prompt_tokens: Number(row.prompt_tokens ?? row.input_length ?? row.input_tokens ?? 0),
    max_tokens: Number(row.max_tokens ?? row.output_length ?? row.output_tokens ?? 0),
  }));
}

export function traceDurationSeconds(events, timeScale = 1) {
  if (!events?.length) return 0;
  const scale = Number(timeScale) > 0 ? Number(timeScale) : 1;
  return Math.max(1, Math.ceil(events.at(-1).timestamp_ms / scale / 1000) + 1);
}
