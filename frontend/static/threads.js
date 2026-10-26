// Threads page: every comment thread of a project, grouped by file
(function () {
    'use strict';

    const filters = ['open', 'you', 'claude', 'resolved'];
    // Lines of context shown first around a comment, and added per "more" click
    const contextLines = 3;
    const contextStep = 10;

    let resolved = resolvedComments || [];
    let filter = 'open';
    // Set when a live update arrives while the user is typing, so the page reloads afterwards
    let pendingReload = false;
    // Root comment ID -> {from, to} line range of the context being shown
    const contextShown = new Map();
    // File path -> Promise of its lines
    const fileLines = new Map();

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    function init() {
        if (!Array.isArray(openComments)) {
            openComments = [];
        }
        const requested = new URLSearchParams(window.location.search).get('filter');
        if (filters.includes(requested)) {
            filter = requested;
        }
        document.querySelectorAll('.thread-filter').forEach((button) => {
            button.addEventListener('click', () => setFilter(button.dataset.filter));
        });
        render();
        setupSSE();
    }

    function setFilter(value) {
        filter = value;
        // Keep the filter in the URL, so live reloads and shared links keep it
        const url = new URL(window.location.href);
        if (value === 'open') {
            url.searchParams.delete('filter');
        } else {
            url.searchParams.set('filter', value);
        }
        history.replaceState(null, '', url);
        render();
    }

    // Threads

    function groupThreads(comments, isResolved) {
        const threads = new Map();
        comments.filter((c) => !c.root_id).forEach((c) => threads.set(c.id, { root: c, replies: [] }));
        comments
            .filter((c) => c.root_id && threads.has(c.root_id))
            .forEach((c) => threads.get(c.root_id).replies.push(c));
        return [...threads.values()].map((thread) => {
            const last = thread.replies.length ? thread.replies[thread.replies.length - 1] : thread.root;
            // Whoever wrote last is waiting on the other side
            thread.status = isResolved ? 'resolved' : last.author === 'user' ? 'claude' : 'you';
            thread.last = last;
            return thread;
        });
    }

    function render() {
        const open = groupThreads(openComments, false);
        const all = { open, resolved: groupThreads(resolved, true) };
        all.you = open.filter((t) => t.status === 'you');
        all.claude = open.filter((t) => t.status === 'claude');

        document.querySelectorAll('.thread-filter').forEach((button) => {
            const value = button.dataset.filter;
            button.classList.toggle('active', value === filter);
            button.setAttribute('aria-pressed', String(value === filter));
            let count = button.querySelector('.filter-count');
            if (!count) {
                count = document.createElement('span');
                count.className = 'filter-count';
                button.appendChild(count);
            }
            count.textContent = all[value].length;
        });

        const threads = all[filter];
        const byFile = new Map();
        for (const thread of threads) {
            const file = thread.root.file_path;
            if (!byFile.has(file)) byFile.set(file, []);
            byFile.get(file).push(thread);
        }

        const list = document.getElementById('thread-list');
        list.replaceChildren();
        for (const file of [...byFile.keys()].sort()) {
            const fileThreads = byFile
                .get(file)
                .sort((a, b) =>
                    filter === 'resolved'
                        ? new Date(b.root.resolved_at) - new Date(a.root.resolved_at)
                        : (a.root.line_start || 0) - (b.root.line_start || 0),
                );
            list.appendChild(createFileGroup(file, fileThreads));
        }

        const empty = document.getElementById('threads-empty');
        empty.hidden = threads.length > 0;
        empty.textContent = {
            open: 'No open threads in this project.',
            you: 'Nothing is waiting on you.',
            claude: 'Nothing is waiting on Claude.',
            resolved: 'No resolved threads yet.',
        }[filter];
    }

    function createFileGroup(file, threads) {
        const group = document.createElement('section');
        group.className = 'thread-file-group';

        const header = document.createElement('h2');
        header.className = 'thread-file-header';
        const link = document.createElement('a');
        link.className = 'thread-file-path';
        link.href = fileURL(file);
        link.textContent = file;
        const count = document.createElement('span');
        count.className = 'thread-file-count';
        count.textContent = `${threads.length} thread${threads.length === 1 ? '' : 's'}`;
        header.append(link, count);
        group.appendChild(header);

        threads.forEach((thread) => group.appendChild(createCard(thread)));
        return group;
    }

    function createCard(thread) {
        const root = thread.root;
        const card = document.createElement('article');
        card.className = 'thread-card';
        card.dataset.rootId = root.id;

        const header = document.createElement('div');
        header.className = 'thread-card-header';
        const chip = document.createElement('span');
        chip.className = `status-chip status-${thread.status}`;
        chip.textContent = {
            you: 'Waiting on you',
            claude: root.claimed ? 'Claude is working on it' : 'Waiting on Claude',
            resolved: 'Resolved',
        }[thread.status];
        const where = document.createElement('span');
        where.textContent = [lineLabel(root), metaLabel(thread)].filter(Boolean).join(' · ');
        const open = document.createElement('a');
        open.className = 'thread-open-link';
        open.href = threadURL(thread);
        open.textContent = 'Open in file →';
        header.append(chip, where, open);
        card.appendChild(header);

        if (root.selected_text) {
            const quote = document.createElement('div');
            quote.className = 'thread-quote';
            quote.textContent = root.selected_text;
            card.appendChild(quote);
        }
        if (root.line_start) {
            card.appendChild(createContext(root));
        }

        for (const message of [root, ...thread.replies]) {
            card.appendChild(createMessage(message));
        }

        if (thread.status !== 'resolved') {
            const actions = document.createElement('div');
            actions.className = 'thread-card-actions';
            actions.append(
                button('Reply', 'comment-badge-reply', () => showReplyForm(card, root)),
                button('Resolve', 'comment-badge-resolve', () => resolveThread(thread)),
            );
            card.appendChild(actions);
        }
        return card;
    }

    function lineLabel(root) {
        if (!root.line_start) return '';
        return root.line_end && root.line_end !== root.line_start
            ? `lines ${root.line_start}–${root.line_end}`
            : `line ${root.line_start}`;
    }

    function metaLabel(thread) {
        if (thread.status === 'resolved') {
            const by = thread.root.resolved_by ? ` by ${capitalizeFirst(thread.root.resolved_by)}` : '';
            return `resolved${by} ${formatRelativeTime(thread.root.resolved_at)}`;
        }
        return formatRelativeTime(thread.last.created_at);
    }

    function createMessage(message) {
        const item = document.createElement('div');
        item.className = 'thread-item';

        const author = document.createElement('div');
        author.className = 'comment-author';
        if (message.author !== 'user') {
            author.classList.add('author-agent');
        }
        const info = document.createElement('div');
        info.className = 'comment-author-info';
        const name = document.createElement('span');
        name.textContent = message.author === 'agent' ? 'Claude' : capitalizeFirst(message.author);
        const time = document.createElement('span');
        time.className = 'comment-timestamp';
        time.textContent = formatRelativeTime(message.created_at);
        info.append(name, time);
        author.appendChild(info);
        item.appendChild(author);

        const body = document.createElement('div');
        body.className = 'thread-item-comment';
        body.innerHTML = message.rendered_html;
        item.appendChild(body);
        return item;
    }

    // Links

    function encodePath(path) {
        return path.split('/').map(encodeURIComponent).join('/');
    }

    function fileURL(file) {
        return `/projects${encodePath(projectDir)}/${encodePath(file)}`;
    }

    // threadURL opens the thread where it is shown: in the Markdown viewer, on the compare page
    // for other files, or at the file itself when neither shows it.
    function threadURL(thread) {
        const file = thread.root.file_path;
        if (thread.status === 'resolved') {
            return fileURL(file);
        }
        if (file.toLowerCase().endsWith('.md')) {
            return `${fileURL(file)}#comment-${thread.root.id}`;
        }
        if (isGitRepo) {
            return `/compare${encodePath(projectDir)}#comment-${thread.root.id}`;
        }
        return fileURL(file);
    }

    // File context

    function createContext(root) {
        const wrapper = document.createElement('div');
        const toggle = document.createElement('button');
        toggle.type = 'button';
        toggle.className = 'thread-context-toggle';
        const panel = document.createElement('div');
        panel.className = 'thread-context';
        wrapper.append(toggle, panel);

        const update = () => {
            const shown = contextShown.has(root.id);
            toggle.textContent = shown ? '▾ Hide context' : '▸ Show context';
            toggle.setAttribute('aria-expanded', String(shown));
            panel.hidden = !shown;
            if (shown) renderContext(panel, root);
        };
        toggle.addEventListener('click', () => {
            if (contextShown.has(root.id)) {
                contextShown.delete(root.id);
            } else {
                const end = root.line_end || root.line_start;
                contextShown.set(root.id, {
                    from: Math.max(1, root.line_start - contextLines),
                    to: end + contextLines,
                });
            }
            update();
        });
        update();
        return wrapper;
    }

    function loadLines(file) {
        if (!fileLines.has(file)) {
            const params = new URLSearchParams({ project_directory: projectDir, file_path: file });
            const promise = fetch(`/api/file-lines?${params}`).then(async (response) => {
                if (!response.ok) {
                    throw new Error(response.status === 404 ? 'File not found' : (await response.text()).trim());
                }
                return (await response.json()).lines || [];
            });
            // A failed load can be retried on the next toggle
            promise.catch(() => fileLines.delete(file));
            fileLines.set(file, promise);
        }
        return fileLines.get(file);
    }

    async function renderContext(panel, root) {
        if (!panel.hasChildNodes()) {
            panel.appendChild(note('Loading…'));
        }
        let lines;
        try {
            lines = await loadLines(root.file_path);
        } catch (error) {
            panel.replaceChildren(note(`Can't show context: ${error.message}`));
            return;
        }
        const range = contextShown.get(root.id);
        if (!range) return;
        const to = Math.min(range.to, lines.length);
        const end = root.line_end || root.line_start;

        const table = document.createElement('table');
        table.className = 'context-table';
        // Fixed layout takes column widths from the first row, which may be a full-width "more" row
        const columns = document.createElement('colgroup');
        const numColumn = document.createElement('col');
        numColumn.className = 'context-num-col';
        columns.append(numColumn, document.createElement('col'));
        table.appendChild(columns);
        const body = document.createElement('tbody');
        if (range.from > 1) {
            body.appendChild(moreRow('↑ Show more', () => (range.from = Math.max(1, range.from - contextStep))));
        }
        for (let n = range.from; n <= to; n++) {
            const row = document.createElement('tr');
            if (n >= root.line_start && n <= end) row.className = 'context-hit';
            const num = document.createElement('td');
            num.className = 'context-num';
            num.textContent = n;
            const text = document.createElement('td');
            text.textContent = lines[n - 1];
            row.append(num, text);
            body.appendChild(row);
        }
        if (to < lines.length) {
            body.appendChild(moreRow('↓ Show more', () => (range.to += contextStep)));
        }
        table.appendChild(body);

        const nodes = [table];
        if (root.line_start > lines.length) {
            nodes.unshift(note(`The file now has only ${lines.length} lines; it has changed since this comment.`));
        }
        panel.replaceChildren(...nodes);

        function moreRow(label, extend) {
            const row = document.createElement('tr');
            row.className = 'context-more';
            const cell = document.createElement('td');
            cell.colSpan = 2;
            const more = document.createElement('button');
            more.type = 'button';
            more.textContent = label;
            more.addEventListener('click', () => {
                extend();
                renderContext(panel, root);
            });
            cell.appendChild(more);
            row.appendChild(cell);
            return row;
        }
    }

    function note(text) {
        const p = document.createElement('p');
        p.className = 'context-note';
        p.textContent = text;
        return p;
    }

    // Actions

    function showReplyForm(card, root) {
        const existing = card.querySelector('.diff-thread-form');
        if (existing) {
            existing.querySelector('textarea').focus();
            return;
        }

        const form = document.createElement('form');
        form.className = 'diff-thread-form';
        const textarea = document.createElement('textarea');
        textarea.rows = 3;
        textarea.placeholder = 'Reply…';
        const buttons = document.createElement('div');
        buttons.className = 'comment-popup-buttons';
        const cancel = document.createElement('button');
        cancel.type = 'button';
        cancel.className = 'comment-btn';
        cancel.textContent = 'Cancel';
        const submit = document.createElement('button');
        submit.type = 'submit';
        submit.className = 'comment-btn comment-btn-primary';
        submit.textContent = 'Reply';
        buttons.append(cancel, submit);
        form.append(textarea, buttons);

        const close = () => {
            form.remove();
            reloadIfPending();
        };
        cancel.addEventListener('click', close);
        form.addEventListener('submit', async (e) => {
            e.preventDefault();
            const text = textarea.value.trim();
            if (!text) return;
            submit.disabled = true;
            const saved = await mutate(() =>
                api('POST', '/api/comments', {
                    project_directory: projectDir,
                    file_path: root.file_path,
                    comment_text: text,
                    root_id: root.id,
                    author: 'user',
                }),
            );
            if (saved) {
                openComments.push(saved);
                form.remove();
                render();
                reloadIfPending();
            } else {
                submit.disabled = false;
            }
        });
        textarea.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                form.requestSubmit();
            } else if (e.key === 'Escape') {
                close();
            }
        });

        card.querySelector('.thread-card-actions').before(form);
        textarea.focus();
    }

    async function resolveThread(thread) {
        if (!confirm('Are you sure you want to resolve this thread?')) return;
        const rootId = thread.root.id;
        if (await mutate(() => api('PATCH', `/api/comments/${rootId}/resolve`, {}))) {
            const now = new Date().toISOString();
            const moved = openComments.filter((c) => c.id === rootId || c.root_id === rootId);
            openComments = openComments.filter((c) => !moved.includes(c));
            resolved = [...moved.map((c) => ({ ...c, resolved_at: now, resolved_by: 'user' })), ...resolved];
            render();
        }
    }

    function button(label, className, onClick) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = `comment-badge-btn ${className}`;
        b.textContent = label;
        b.addEventListener('click', onClick);
        return b;
    }

    // mutate runs an API change, reporting failures, and returns its result (null on failure).
    async function mutate(fn) {
        try {
            return (await fn()) || true;
        } catch (error) {
            console.error('Request failed:', error);
            alert(`Request failed: ${error.message}`);
            return null;
        }
    }

    async function api(method, url, body) {
        const response = await fetch(url, {
            method,
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        if (!response.ok) {
            throw new Error((await response.text()) || `HTTP ${response.status}`);
        }
        const text = await response.text();
        return text ? JSON.parse(text) : null;
    }

    // Live updates

    function setupSSE() {
        const params = new URLSearchParams({ project_directory: projectDir, scope: 'project' });
        const eventSource = new EventSource(`/api/events?${params}`);

        ['comments_resolved', 'threads_claimed', 'reload'].forEach((event) => {
            eventSource.addEventListener(event, requestReload);
        });

        eventSource.onerror = () => {
            eventSource.close();
            setTimeout(setupSSE, 5000);
        };
    }

    function requestReload() {
        const typing = [...document.querySelectorAll('.diff-thread-form textarea')].some((t) => t.value.trim() !== '');
        if (typing) {
            pendingReload = true;
        } else {
            window.location.reload();
        }
    }

    function reloadIfPending() {
        if (pendingReload && !document.querySelector('.diff-thread-form')) {
            window.location.reload();
        }
    }

    // Formatting (same as viewer.js)

    function capitalizeFirst(s) {
        if (!s) return '';
        return s.charAt(0).toUpperCase() + s.slice(1);
    }

    function formatRelativeTime(timestamp) {
        if (!timestamp) return '';
        const then = new Date(timestamp);
        if (isNaN(then.getTime())) return '';

        const diffMins = Math.floor((new Date() - then) / 60000);
        const diffHours = Math.floor(diffMins / 60);
        const diffDays = Math.floor(diffHours / 24);
        if (diffMins < 1) return 'just now';
        if (diffMins < 60) return `${diffMins}m ago`;
        if (diffHours < 24) return `${diffHours}h ago`;
        if (diffDays < 7) return `${diffDays}d ago`;
        return then.toLocaleDateString();
    }
})();
