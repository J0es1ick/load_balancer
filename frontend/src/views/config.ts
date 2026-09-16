import type { ConsoleState } from '../state.js'
import { escapeHTML, formatDate } from '../ui/html.js'

export function renderConfig(state: ConsoleState, mode: 'demo' | 'live'): string {
  const status = state.status
  const validation = state.validation
  const canMutate = Boolean(status?.runtime_mutations_enabled) && status?.principal?.role === 'admin'
  return `
    <div class="config-layout">
      <section class="panel editor-panel">
        <header class="panel-header">
          <div><span class="eyebrow">proxy/v1</span><h2>GatewayConfig</h2></div>
          <div class="editor-actions">
            <button class="button" data-action="format-config">Format</button>
            <button class="button" data-action="validate-config">Validate</button>
            <button class="button button--primary" data-action="apply-config" ${!canMutate || !state.editorDirty ? 'disabled' : ''}>Apply revision</button>
          </div>
        </header>
        ${status?.principal?.role !== 'admin' ? '<div class="permission-note permission-note--editor">Validate доступен всем ролям; apply и rollback требуют admin.</div>' : ''}
        <textarea class="config-editor" id="config-editor" spellcheck="false" aria-label="GatewayConfig JSON">${escapeHTML(state.editorText)}</textarea>
        <footer class="editor-footer">
          <span class="editor-state ${validation ? (validation.valid ? 'is-valid' : 'is-invalid') : ''}">${
            validation ? (validation.valid ? '● schema valid' : `● ${validation.errors?.length ?? 1} validation errors`) : state.editorDirty ? '○ not validated' : '● matches runtime'
          }</span>
          <span>optimistic lock: revision ${status?.gateway.revision ?? '—'}</span>
        </footer>
      </section>
      <aside class="config-sidebar">
        <section class="panel revision-card">
          <span class="eyebrow">Current snapshot</span>
          <strong>r${status?.gateway.revision ?? '—'}</strong>
          <code>${escapeHTML(status?.gateway.hash ?? '—')}</code>
          <dl><div><dt>applied</dt><dd>${formatDate(status?.gateway.applied_at)}</dd></div><div><dt>previous</dt><dd>${status?.gateway.previous_revision ?? 'none'}</dd></div><div><dt>history limit</dt><dd>${state.config?.history_limit ?? '—'}</dd></div></dl>
          <button class="button button--danger" data-action="rollback-config" ${!canMutate || status?.gateway.previous_revision == null ? 'disabled' : ''}>Rollback previous</button>
        </section>
        <section class="panel validation-card">
          <span class="eyebrow">Validation</span>
          ${
            validation?.errors?.length
              ? `<ol>${validation.errors.map((issue) => `<li><code>${escapeHTML(issue.path)}</code><span>${escapeHTML(issue.message)}</span></li>`).join('')}</ol>`
              : `<p>${
                  validation?.valid
                    ? mode === 'demo'
                      ? 'Конфиг прошёл проверку детерминированного browser simulator.'
                      : 'Конфиг прошёл server-side validation management API.'
                    : 'Нажмите Validate перед Apply. Неизвестные поля и ссылки на несуществующие clusters отклоняются.'
                }</p>`
          }
        </section>
        ${
          mode === 'demo'
            ? `<label class="panel persistence-card"><input type="checkbox" data-demo-persistence ${state.persistence ? 'checked' : ''}/><span><strong>Remember demo config</strong><small>Хранить snapshot только в localStorage этого browser.</small></span></label>`
            : ''
        }
      </aside>
    </div>
  `
}
