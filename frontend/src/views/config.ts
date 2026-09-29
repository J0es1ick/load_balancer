import type { ConsoleState } from "../state.js";
import { escapeHTML, formatDate } from "../ui/html.js";

export function renderConfig(
  state: ConsoleState,
  mode: "demo" | "live",
): string {
  const status = state.status;
  const validation = state.validation;
  const canMutate =
    Boolean(status?.runtime_mutations_enabled) &&
    status?.principal?.role === "admin";
  return `
    <div class="config-layout">
      <section class="panel editor-panel">
        <header class="panel-header">
          <div><span class="eyebrow">Черновик · proxy/v1</span><h2>GatewayConfig</h2></div>
          <div class="editor-actions">
            <button class="button" data-action="format-config">Форматировать</button>
            <button class="button" data-action="validate-config">Проверить</button>
            <button class="button button--primary" data-action="apply-config" ${!canMutate || !state.editorDirty ? "disabled" : ""}>Применить</button>
          </div>
        </header>
        ${status?.principal?.role !== "admin" ? '<div class="permission-note permission-note--editor">Проверка доступна всем ролям; применение и откат требуют роль admin.</div>' : ""}
        <textarea class="config-editor" id="config-editor" spellcheck="false" aria-label="GatewayConfig JSON">${escapeHTML(state.editorText)}</textarea>
        <footer class="editor-footer">
          <span class="editor-state ${validation ? (validation.valid ? "is-valid" : "is-invalid") : ""}">${
            validation
              ? validation.valid
                ? "● структура корректна"
                : `● ошибок: ${validation.errors?.length ?? 1}`
              : state.editorDirty
                ? "○ не проверено"
                : "● совпадает с конфигурацией версии"
          }</span>
          <span>${state.editorDirty ? "Есть неприменённые изменения" : "Черновик без изменений"} · r${status?.gateway.revision ?? "—"}</span>
        </footer>
      </section>
      <aside class="config-sidebar">
        <section class="panel revision-card">
          <span class="eyebrow">Применённая версия</span>
          <strong>r${status?.gateway.revision ?? "—"}</strong>
          <code>${escapeHTML(status?.gateway.hash ?? "—")}</code>
          <dl><div><dt>применена</dt><dd>${formatDate(status?.gateway.applied_at)}</dd></div><div><dt>предыдущая</dt><dd>${status?.gateway.previous_revision ?? "нет"}</dd></div><div><dt>глубина истории</dt><dd>${state.config?.history_limit ?? "—"}</dd></div></dl>
          <button class="button button--danger" data-action="rollback-config" ${!canMutate || status?.gateway.previous_revision == null ? "disabled" : ""}>Откатить конфигурацию</button>
        </section>
        <section class="panel config-lifecycle">
          <span class="eyebrow">Где живут изменения</span>
          <dl>
            <div><dt>Черновик</dt><dd>Текст слева ещё не влияет на запросы. Проверка не применяет изменения.</dd></div>
            <div><dt>Применённая версия</dt><dd>${mode === "demo" ? "Работает в симуляторе этой вкладки." : "Работает в памяти одного Go-процесса. Apply не записывает YAML на диск; после перезапуска читается файл запуска."}</dd></div>
            <div><dt>Временные команды</dt><dd>Включение, отключение и drain в Clusters не меняют JSON. Они сохраняются при смене маршрута или стратегии. Изменение поля disabled явно заменяет такую команду.</dd></div>
          </dl>
          <p>Откат возвращает конфигурацию, но не обнуляет активные запросы и счётчики. Новый URL или ID означает другой сервер с новым состоянием.</p>
        </section>
        <section class="panel validation-card">
          <span class="eyebrow">Проверка структуры</span>
          ${
            validation?.errors?.length
              ? `<ol>${validation.errors.map((issue) => `<li><code>${escapeHTML(issue.path)}</code><span>${escapeHTML(issue.message)}</span></li>`).join("")}</ol>`
              : `<p>${
                  validation?.valid
                    ? mode === "demo"
                      ? "Структура конфигурации проверена симулятором. Это не тест настоящих серверов."
                      : "Структура проверена management API. Доступность новых серверов проверяется отдельно при применении, если включён health-check."
                    : "Нажмите «Проверить» перед применением. Неизвестные поля и ссылки на несуществующие группы серверов отклоняются."
                }</p>`
          }
        </section>
        ${
          mode === "demo"
            ? `<label class="panel persistence-card"><input type="checkbox" data-demo-persistence ${state.persistence ? "checked" : ""}/><span><strong>Запомнить demo-конфигурацию</strong><small>Сохранить применённый JSON в этом браузере. Черновик, drain и счётчики не сохраняются.</small></span></label>`
            : ""
        }
      </aside>
    </div>
  `;
}
