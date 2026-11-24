<script lang="ts">
  import { onMount } from "svelte";
  type Item = {
    name: string;
    unit: string;
    quantity: number;
    location: string;
    expiry: string;
    category: string;
  };
  type Op = Item & { action: string; destination?: string };
  type Plan = { mode: string; query?: string; question?: string; ops?: Op[] };
  let lang = $state(
    localStorage.getItem("lang") ||
      (navigator.language.startsWith("ru") ? "ru" : "en"),
  );
  const tr = (ru: string, en: string) => (lang === "ru" ? ru : en);
  const labels: Record<string, [string, string]> = {
    list: ["Запасы", "Inventory"],
    shopping: ["Купить", "Shopping"],
    expire: ["Сроки", "Expiring"],
    history: ["История", "History"],
    set: ["Осталось", "Set quantity"],
    add: ["Добавить", "Add"],
    consume: ["Израсходовать", "Consume"],
    move: ["Переместить", "Move"],
    food: ["Продукты", "Food"],
    medicine: ["Аптечка", "Medicine"],
    supplies: ["Расходники", "Supplies"],
    other: ["Прочее", "Other"],
  };
  const label = (k: string) => (labels[k] ? tr(...labels[k]) : k);
  const blank = (): Op => ({
    action: "set",
    name: "",
    quantity: 1,
    unit: "pcs",
    location: "",
    expiry: "",
    category: "food",
    destination: "",
  });
  let token = $state(sessionStorage.getItem("token") || ""),
    connected = $state(false),
    busy = $state(false),
    error = $state(""),
    notice = $state("");
  let tab = $state("list"),
    query = $state(""),
    command = $state(""),
    rows = $state<Item[]>([]),
    history = $state<{ at: string; ops: Op[] }[]>([]);
  let editing = $state<number | null>(null);
  let manual = $state(false),
    form = $state<Op>(blank()),
    draft = $state<Op[]>([]),
    draftId = $state("");
  let shown = $derived(
    rows.filter((i) => i.name.toLowerCase().includes(query.toLowerCase())),
  );
  $effect(() => {
    localStorage.setItem("lang", lang);
    document.documentElement.lang = lang;
  });
  async function api(path: string, body?: unknown) {
    let file = body instanceof File;
    let res = await fetch("/api/" + path, {
      method: body === undefined ? "GET" : "POST",
      headers: {
        Authorization: "Bearer " + token,
        "Content-Type": file ? "application/octet-stream" : "application/json",
      },
      body: file ? (body as File) : JSON.stringify(body),
    });
    if (res.status === 401) {
      logout();
      throw Error(tr("Проверьте ключ доступа.", "Check your access token."));
    }
    if (!res.ok)
      throw Error(
        res.status === 503
          ? tr("Проверьте подключение к модели.", "Check the model connection.")
          : tr(
              "Запрос отклонён. Проверьте данные и остаток.",
              "Request failed. Check the values and available stock.",
            ),
      );
    return res.json();
  }
  async function run(fn: () => Promise<void>) {
    if (busy) return;
    busy = true;
    error = "";
    notice = "";
    try {
      await fn();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }
  async function refresh(next = tab) {
    if (next === "history") history = await api("history");
    else rows = await api("items?mode=" + next);
    tab = next;
  }
  async function login() {
    await refresh("list");
    connected = true;
    sessionStorage.setItem("token", token);
  }
  function logout() {
    editing = null;
    connected = false;
    sessionStorage.removeItem("token");
    token = "";
    draft = [];
    rows = [];
    history = [];
    command = "";
    form = blank();
    query = "";
    notice = "";
    manual = false;
  }
  onMount(() => {
    if (token) run(login);
  });
  function preview(ops: Op[]) {
    editing = null;
    draft = ops;
    draftId = Array.from(crypto.getRandomValues(new Uint8Array(16)), (n) =>
      n.toString(16).padStart(2, "0"),
    ).join("");
    manual = false;
  }
  async function interpret() {
    let p: Plan = await api("interpret", { text: command });
    if (p.mode === "write" && p.ops) preview(p.ops);
    else if (p.mode === "clarify") notice = p.question || "";
    else {
      await refresh(p.mode);
      query = p.query || "";
    }
  }
  async function save() {
    await api("apply", { id: draftId, ops: draft });
    draft = [];
    command = "";
    form = blank();
    query = "";
    notice = tr("Сохранено", "Saved");
    await refresh("list");
  }
  function edit(i: Item) {
    editing = null;
    form = { ...i, action: "set" };
    manual = true;
    draft = [];
  }
  function describe(o: Op) {
    return [
      label(o.action),
      o.name,
      o.quantity + " " + o.unit,
      o.location,
      o.destination,
      o.expiry,
      label(o.category),
    ]
      .filter(Boolean)
      .join(" · ");
  }
</script>

      {#snippet opForm()}
<form
          class="panel grid"
          onsubmit={(e) => {
            e.preventDefault();
            if (editing === null) preview([{ ...form }]);
            else {
              draft[editing] = { ...form, destination: form.action === "move" ? form.destination : "" };
              editing = null;
            }
          }}
        >
          <label
            >{tr("Действие", "Action")}<select bind:value={form.action}
              >{#each ["set", "add", "consume", "move"] as a}<option value={a}
                  >{label(a)}</option
                >{/each}</select
            ></label
          >
          <label
            >{tr("Название", "Name")}<input
              bind:value={form.name}
              required
            /></label
          >
          <label
            >{tr("Количество", "Quantity")}<input
              type="number"
              min="0"
              max="1000000000"
              step="any"
              bind:value={form.quantity}
              required
            /></label
          >
          <label
            >{tr("Единица", "Unit")}<input
              bind:value={form.unit}
              required
              list="units"
            /><datalist id="units"
              >{#each ["pcs", "kg", "g", "l", "ml"] as unit}<option value={unit}
                ></option>{/each}</datalist
            ></label
          >
          <label
            >{tr("Категория", "Category")}<select bind:value={form.category}
              >{#each ["food", "medicine", "supplies", "other"] as a}<option
                  value={a}>{label(a)}</option
                >{/each}</select
            ></label
          >
          <label
            >{tr("Место", "Location")}<input
              bind:value={form.location}
            /></label
          >
          {#if form.action === "move"}<label
              >{tr("Куда", "Destination")}<input
                bind:value={form.destination}
                required
              /></label
            >{/if}
          <label
            >{tr("Годен до", "Expiry")}<input
              type="date"
              bind:value={form.expiry}
            /></label
          ><button class="primary">{editing === null ? tr("Проверить", "Review") : tr("Сохранить строку", "Save row")}</button>
          {#if editing !== null}<button type="button" onclick={() => editing = null}>{tr("Отменить правку", "Discard edit")}</button>{/if}
        </form>
{/snippet}

<header>
  <strong><img src="/icon.svg" alt="" /> AIList</strong>
  <div>
    <button class="plain" onclick={() => (lang = lang === "ru" ? "en" : "ru")}
      >{lang === "ru" ? "EN" : "RU"}</button
    >{#if connected}<button class="plain" onclick={logout} disabled={busy}
        >{tr("Выйти", "Sign out")}</button
      >{/if}
  </div>
</header>
<main>
  {#if !connected}
    <section class="login">
      <h1>{tr("Домашние запасы", "Household inventory")}</h1>
      <form
        onsubmit={(e) => {
          e.preventDefault();
          run(login);
        }}
      >
        <label
          >{tr("Ключ доступа", "Access token")}<input
            type="password"
            bind:value={token}
            autocomplete="current-password"
            required
          /></label
        ><button class="primary" disabled={busy}
          >{tr("Войти", "Sign in")}</button
        >
      </form>
    </section>
  {:else}
    <h1>{tr("Домашние запасы", "Household inventory")}</h1>
    <fieldset disabled={busy}>
      <section class="compose">
        <form
          onsubmit={(e) => {
            e.preventDefault();
            run(interpret);
          }}
        >
          <label for="cmd"
            >{tr("Сообщение помощнику", "Inventory assistant")}</label
          ><textarea
            id="cmd"
            placeholder={tr(
              "Муки осталось 2 кг, колбаса кончилась…",
              "Two kilos of flour left, out of sausages…",
            )}
            bind:value={command}
            required
          ></textarea>
          <div class="actions">
            <button class="primary">{tr("Разобрать", "Interpret")}</button
            ><label class="upload"
              >{tr("Аудиофайл", "Audio file")}<input
                type="file"
                accept="audio/*"
                onchange={(e) => {
                  const f = e.currentTarget.files?.[0];
                  e.currentTarget.value = "";
                  if (f)
                    run(async () => {
                      command = (await api("voice", f)).text;
                    });
                }}
              /></label
            >
          </div>
        </form>
      </section>
      {#if draft.length}<section class="panel">
          <h2>{tr("Проверить изменения", "Review changes")}</h2>
          <ul>
            {#each draft as o, i}
              <li>{describe(o)}
                <button onclick={() => { manual = false; editing = i; form = { ...o }; }}>{tr("Исправить", "Edit row")} {i+1}</button>
                {#if editing === i}{@render opForm()}{/if}
              </li>
            {/each}
          </ul>
          <button class="primary" disabled={editing !== null} onclick={() => run(save)}
            >{tr("Подтвердить", "Confirm")}</button
          >
          <button onclick={() => { draft = []; editing = null; }}>{tr("Отмена", "Cancel")}</button>
        </section>{/if}
      <nav>
        {#each ["list", "shopping", "expire", "history"] as k}<button
            class:active={tab === k}
            onclick={() =>
              run(async () => {
                await refresh(k);
                query = "";
              })}>{label(k)}</button
          >{/each}
      </nav>
      <div class="toolbar">
        <h2>{label(tab)}</h2>
        <button
          onclick={() => {
            editing = null;
            form = blank();
            manual = !manual;
          }}>{tr("Внести вручную", "Manual entry")}</button
        >
      </div>
      {#if manual}{@render opForm()}{/if}
      {#if tab === "history"}{#each history as h}<article>
            <small>{new Date(h.at).toLocaleString(lang)}</small
            >{#each h.ops as o}<div>{describe(o)}</div>{/each}
          </article>{:else}<p>{tr("История пуста", "No changes yet")}</p>{/each}
      {:else}
        <input
          class="search"
          placeholder={tr("Поиск по названию", "Search by name")}
          aria-label={tr("Поиск", "Search")}
          bind:value={query}
        />
        {#if tab === "expire"}<small
            >{tr(
              "Просрочено или истекает в ближайшие 7 дней",
              "Expired or due in the next 7 days",
            )}</small
          >{/if}
        <div class="list">
          {#each shown as i}<button class="row" onclick={() => edit(i)}
              ><span
                ><b>{i.name}</b><small
                  >{[label(i.category), i.location, i.expiry]
                    .filter(Boolean)
                    .join(" · ")}</small
                ></span
              ><strong>{i.quantity} {i.unit}</strong></button
            >{:else}<p>{tr("Пока пусто", "No matching items")}</p>{/each}
        </div>{/if}
    </fieldset>{/if}
  {#if busy}<p role="status">
      {tr("Обработка…", "Working…")}
    </p>{/if}{#if error}<aside role="alert">
      {error}
    </aside>{/if}{#if notice}<aside role="status">{notice}</aside>{/if}
</main>
