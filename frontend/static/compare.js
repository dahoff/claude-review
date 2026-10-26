// Compare page: split diffs with inline comment threads
(function () {
    'use strict';

    // Set when a live update arrives while the user is typing, so the page reloads afterwards
    let pendingReload = false;
    let commentButton = null;
    let pendingSelection = null;
    // Root comment ID of the thread selected in the thread list
    let currentThreadId = null;

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }

    function init() {
        if (!Array.isArray(comments)) {
            comments = [];
        }
        document.body.classList.add('js');
        initControls();
        initFileTree();
        initSidebar();
        document.getElementById('general-comment-add').addEventListener('click', showGeneralCommentForm);
        if (!readOnly) {
            initSelection();
        }
        renderThreads();
        scrollToLinkedThread();
        setupSSE();
    }

    // Scroll to the thread named by a #comment-<id> link (from the threads page)
    function scrollToLinkedThread() {
        const match = window.location.hash.match(/^#comment-(\d+)$/);
        if (match) focusThread(Number(match[1]), 'auto');
    }

    // focusThread scrolls to a thread, opening its file if collapsed, and selects it in the thread list
    function focusThread(id, behavior = 'smooth') {
        const thread = document.querySelector(`.diff-thread[data-root-id="${id}"]`);
        if (!thread) return;
        currentThreadId = id;
        history.replaceState(null, '', `#comment-${id}`);
        const section = thread.closest('.diff-file');
        if (section) section.classList.remove('collapsed');
        thread.scrollIntoView({ behavior, block: 'center' });
        renderThreadNav();
    }

    // Sidebar: tabs between the changed files and the open threads, and stepping through threads

    function initSidebar() {
        const sidebar = document.querySelector('.compare-sidebar');
        if (!sidebar) return;
        const tabs = [...sidebar.querySelectorAll('.sidebar-tab')];
        sidebar.querySelector('.sidebar-tabs').hidden = false;
        sidebar.querySelector('.thread-nav-steps').hidden = false;

        const show = (panel) => {
            tabs.forEach((tab) => {
                const active = tab.dataset.panel === panel;
                tab.classList.toggle('active', active);
                tab.setAttribute('aria-selected', String(active));
            });
            sidebar.querySelectorAll('.sidebar-panel').forEach((p) => (p.hidden = p.dataset.panel !== panel));
            try {
                localStorage.setItem('claude-review-compare-sidebar', panel);
            } catch (e) {
                // Storage unavailable: the choice lasts for this page only
            }
        };
        tabs.forEach((tab) => tab.addEventListener('click', () => show(tab.dataset.panel)));

        let saved = null;
        try {
            saved = localStorage.getItem('claude-review-compare-sidebar');
        } catch (e) {
            // Storage unavailable: use the default
        }
        const panels = tabs.map((tab) => tab.dataset.panel);
        show(panels.includes(saved) ? saved : panels[0]);

        sidebar.querySelector('.thread-nav-prev').addEventListener('click', () => stepThread(-1));
        sidebar.querySelector('.thread-nav-next').addEventListener('click', () => stepThread(1));
        document.addEventListener('keydown', (e) => {
            if (e.metaKey || e.ctrlKey || e.altKey || e.isComposing) return;
            if (e.target instanceof Element && e.target.closest('input, textarea, select, [contenteditable]')) return;
            if (e.key === 'n') stepThread(1);
            if (e.key === 'p') stepThread(-1);
        });
    }

    // navThreads returns the open threads in page order, with where each is shown.
    function navThreads() {
        const byId = new Map(groupThreads().map((t) => [t.root.id, t]));
        return [...document.querySelectorAll('.diff-thread[data-root-id]')]
            .map((el) => byId.get(Number(el.dataset.rootId)))
            .filter(Boolean)
            .map((thread) => {
                const last = thread.replies.length ? thread.replies[thread.replies.length - 1] : thread.root;
                return {
                    id: thread.root.id,
                    file: thread.root.file_path,
                    line: thread.root.line_start,
                    status: last.author === 'agent' ? 'you' : 'claude',
                    claimed: thread.root.claimed,
                    outdated: thread.root.outdated,
                    snippet: (thread.root.comment_text || '').trim().split('\n')[0],
                };
            });
    }

    function stepThread(direction) {
        const threads = navThreads();
        if (threads.length === 0) return;
        const index = threads.findIndex((t) => t.id === currentThreadId);
        const next = index === -1 ? threads[direction > 0 ? 0 : threads.length - 1] : threads[index + direction];
        if (next) focusThread(next.id);
    }

    function renderThreadNav() {
        // Mark the selected thread, also after renderThreads rebuilt it
        document.querySelectorAll('.diff-thread[data-root-id]').forEach((t) => {
            t.classList.toggle('linked', Number(t.dataset.rootId) === currentThreadId);
        });
        const sidebar = document.querySelector('.compare-sidebar');
        if (!sidebar) return;
        const threads = navThreads();
        const empty = threads.length === 0;
        const index = threads.findIndex((t) => t.id === currentThreadId);
        sidebar.querySelector('.thread-nav-count').textContent = threads.length;
        sidebar.querySelector('.thread-nav-position').textContent = empty
            ? 'none open'
            : index === -1
              ? `${threads.length} open`
              : `${index + 1} of ${threads.length}`;
        sidebar.querySelector('.thread-nav-prev').disabled = empty || index === 0;
        sidebar.querySelector('.thread-nav-next').disabled = empty || index === threads.length - 1;

        const list = sidebar.querySelector('.thread-nav-list');
        list.replaceChildren();
        if (empty) {
            const note = document.createElement('p');
            note.className = 'thread-nav-empty';
            note.textContent = 'No open threads. Select text on the right side of a diff to comment.';
            list.appendChild(note);
            return;
        }
        let lastFile = null;
        for (const thread of threads) {
            if (thread.file !== lastFile) {
                lastFile = thread.file;
                const heading = document.createElement('div');
                heading.className = 'thread-nav-file';
                heading.textContent = thread.file || 'General';
                heading.title = thread.file || 'Comments on the project, not on a single file';
                list.appendChild(heading);
            }

            const item = document.createElement('a');
            item.className = 'thread-nav-item';
            item.href = `#comment-${thread.id}`;
            if (thread.id === currentThreadId) item.classList.add('current');
            item.addEventListener('click', (e) => {
                e.preventDefault();
                focusThread(thread.id);
            });

            const dot = document.createElement('span');
            dot.className = `thread-nav-dot status-${thread.status}`;
            dot.title =
                thread.status === 'you'
                    ? 'Waiting on you'
                    : thread.claimed
                      ? 'Claude is working on it'
                      : 'Waiting on Claude';
            item.appendChild(dot);
            if (thread.line) {
                const line = document.createElement('span');
                line.className = 'thread-nav-line';
                line.textContent = `L${thread.line}`;
                if (thread.outdated) line.title = 'Outdated: the commented lines changed';
                item.appendChild(line);
            }
            const text = document.createElement('span');
            text.className = 'thread-nav-snippet';
            text.textContent = thread.snippet;
            item.appendChild(text);
            list.appendChild(item);
        }

        const current = list.querySelector('.thread-nav-item.current');
        if (current) current.scrollIntoView({ block: 'nearest' });
    }

    function initControls() {
        // Pickers apply as soon as a value is chosen
        document.querySelectorAll('.compare-picker select').forEach((select) => {
            select.addEventListener('change', () => select.form.submit());
        });

        document.querySelectorAll('.diff-gap-toggle button').forEach((button) => {
            button.addEventListener('click', () => {
                const toggle = button.closest('tbody');
                toggle.nextElementSibling.hidden = false;
                toggle.remove();
            });
        });

        document.querySelectorAll('.diff-file-toggle').forEach((button) => {
            button.addEventListener('click', () => {
                button.closest('.diff-file').classList.toggle('collapsed');
            });
        });
    }

    // File tree

    function initFileTree() {
        const tree = document.querySelector('.file-tree');
        if (!tree) return;
        const links = [...tree.querySelectorAll('.tree-file')];

        // A clicked file stays highlighted until the user scrolls, since files near the end of
        // the page can't scroll to the top. Jumping to a collapsed file opens it.
        let pinned = null;
        links.forEach((link) => {
            link.addEventListener('click', () => {
                const section = document.getElementById(link.getAttribute('href').slice(1));
                if (section) section.classList.remove('collapsed');
                pinned = link;
                links.forEach((l) => l.classList.toggle('active', l === link));
            });
        });
        ['wheel', 'keydown', 'touchmove', 'mousedown'].forEach((event) => {
            window.addEventListener(event, (e) => {
                if (!e.target.closest || !e.target.closest('.file-tree')) pinned = null;
            });
        });

        const filter = tree.querySelector('.file-tree-filter');
        filter.hidden = false;
        filter.addEventListener('input', () => {
            const query = filter.value.trim().toLowerCase();
            links.forEach((link) => {
                link.parentElement.classList.toggle('filtered-out', !link.dataset.path.toLowerCase().includes(query));
            });
            // Hide folders left without any matching file, deepest first
            [...tree.querySelectorAll('details')].reverse().forEach((folder) => {
                const visible = [...folder.querySelectorAll(':scope > ul > li')].some(
                    (li) => !li.classList.contains('filtered-out'),
                );
                folder.parentElement.classList.toggle('filtered-out', !visible);
            });
        });

        // Highlight the file being read: the last one whose header has scrolled past the top
        const sections = [...document.querySelectorAll('.diff-file')];
        let scheduled = false;
        const highlight = () => {
            scheduled = false;
            if (pinned) return;
            let current = sections[0];
            for (const section of sections) {
                if (section.getBoundingClientRect().top <= 80) current = section;
            }
            links.forEach((link) =>
                link.classList.toggle('active', current && link.getAttribute('href') === `#${current.id}`),
            );
        };
        window.addEventListener('scroll', () => {
            if (!scheduled) {
                scheduled = true;
                requestAnimationFrame(highlight);
            }
        });
        highlight();
    }

    // Selection

    function initSelection() {
        commentButton = document.createElement('button');
        commentButton.type = 'button';
        commentButton.className = 'comment-button';
        commentButton.textContent = 'Comment';
        commentButton.style.display = 'none';
        commentButton.addEventListener('mousedown', (e) => e.preventDefault());
        commentButton.addEventListener('click', () => {
            const selection = pendingSelection;
            hideCommentButton();
            window.getSelection().removeAllRanges();
            if (selection) {
                showNewCommentForm(selection);
            }
        });
        document.body.appendChild(commentButton);

        // Restrict selection to one side of the table, so dragging across rows
        // selects only that side's text
        document.addEventListener('mousedown', (e) => {
            if (e.target === commentButton) return;
            hideCommentButton();
            const cell = e.target.closest && e.target.closest('.diff-table td');
            const table = cell && cell.closest('.diff-table');
            document.querySelectorAll('.diff-table').forEach((t) => {
                t.classList.remove('select-new', 'select-old');
            });
            if (table) {
                const side = cell.classList.contains('diff-old') || cell.classList.contains('diff-num-old');
                table.classList.add(side ? 'select-old' : 'select-new');
            }
        });

        document.addEventListener('mouseup', (e) => {
            if (e.target === commentButton) return;
            // Let the browser finish updating the selection
            setTimeout(() => handleSelection(e), 0);
        });
    }

    function handleSelection(event) {
        const selection = window.getSelection();
        const text = selection.toString().trim();
        if (!text || selection.rangeCount === 0) {
            return;
        }

        const range = selection.getRangeAt(0);
        const startRow = rowOf(range.startContainer);
        const endRow = rowOf(range.endContainer);
        if (!startRow || !endRow) {
            return;
        }

        const section = startRow.closest('.diff-file');
        if (!section || section !== endRow.closest('.diff-file')) {
            return;
        }

        // Rows whose new-side text is part of the selection. A triple-click ends the range at the
        // start of the next row, which touches that row without selecting any of it.
        const lines = [];
        section.querySelectorAll('tr.diff-row[data-new-line]').forEach((row) => {
            if (range.intersectsNode(row) && selectedTextIn(range, row.querySelector('td.diff-new'))) {
                lines.push(parseInt(row.dataset.newLine, 10));
            }
        });
        if (lines.length === 0) {
            return;
        }

        pendingSelection = {
            section,
            filePath: section.dataset.path,
            lineStart: Math.min(...lines),
            lineEnd: Math.max(...lines),
            selectedText: text,
        };
        showCommentButton(event.clientX, event.clientY);
    }

    // selectedTextIn returns the part of cell's text that lies inside range.
    function selectedTextIn(range, cell) {
        const part = document.createRange();
        part.selectNodeContents(cell);
        if (range.compareBoundaryPoints(Range.START_TO_START, part) > 0) {
            part.setStart(range.startContainer, range.startOffset);
        }
        if (range.compareBoundaryPoints(Range.END_TO_END, part) < 0) {
            part.setEnd(range.endContainer, range.endOffset);
        }
        return part.toString().trim();
    }

    // rowOf returns the diff row containing node when node is in a new-side cell.
    function rowOf(node) {
        const element = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
        const cell = element && element.closest('td.diff-new');
        return cell ? cell.closest('tr.diff-row') : null;
    }

    function showCommentButton(x, y) {
        commentButton.style.left = `${x + 8}px`;
        commentButton.style.top = `${y + 8}px`;
        commentButton.style.display = 'block';
    }

    function hideCommentButton() {
        if (commentButton) {
            commentButton.style.display = 'none';
        }
        pendingSelection = null;
    }

    function showNewCommentForm(selection) {
        const holder = threadsCellFor(selection.section, selection.lineEnd);
        const formContainer = document.createElement('div');
        formContainer.className = 'diff-thread';
        const quote = document.createElement('div');
        quote.className = 'thread-item-text';
        quote.textContent = `"${selection.selectedText}"`;
        formContainer.appendChild(quote);
        holder.appendChild(formContainer);

        showForm(formContainer, '', 'Comment', async (text) => {
            const saved = await api('POST', '/api/comments', {
                project_directory: projectDir,
                file_path: selection.filePath,
                line_start: selection.lineStart,
                line_end: selection.lineEnd,
                selected_text: selection.selectedText,
                comment_text: text,
            });
            comments.push(saved);
            placements[saved.id] = { file: selection.filePath, line: selection.lineEnd, where: 'line' };
        });
    }

    // A general comment is about the project rather than one file, e.g. a file that should have changed
    function showGeneralCommentForm() {
        const section = document.getElementById('general-comments');
        const holder = section.querySelector('.general-comment-new');
        section.hidden = false;
        section.scrollIntoView({ block: 'nearest' });
        showForm(holder, '', 'Comment', async (text) => {
            const saved = await api('POST', '/api/comments', {
                project_directory: projectDir,
                file_path: '',
                comment_text: text,
            });
            comments.push(saved);
        });
        holder.querySelector('textarea').placeholder =
            'A comment on the project, not on a single file (e.g. "You should also have updated README.md")';
    }

    // Threads

    function renderThreads() {
        document.querySelectorAll('.diff-thread-row').forEach((row) => row.remove());
        document.querySelectorAll('.diff-file-comments').forEach((el) => el.replaceChildren());
        const otherSection = document.getElementById('other-comments');
        const otherList = otherSection.querySelector('.other-comments-list');
        otherList.replaceChildren();
        const generalSection = document.getElementById('general-comments');
        const generalList = generalSection.querySelector('.general-comments-list');
        generalList.replaceChildren();

        const otherByFile = new Map();
        for (const thread of groupThreads()) {
            if (!thread.root.file_path) {
                generalList.appendChild(createThread(thread, false));
                continue;
            }
            const placement = placements[thread.root.id] || { file: thread.root.file_path, where: 'other' };
            const section = sectionFor(placement.file);

            if (placement.where === 'line' && section) {
                threadsCellFor(section, placement.line).appendChild(createThread(thread, false));
            } else if (placement.where !== 'other' && section) {
                const list = section.querySelector('.diff-file-comments');
                if (!list.hasChildNodes()) {
                    const heading = document.createElement('h3');
                    heading.textContent = 'Comments on lines not shown';
                    list.appendChild(heading);
                }
                list.appendChild(createThread(thread, true));
            } else {
                if (!otherByFile.has(placement.file)) {
                    otherByFile.set(placement.file, []);
                }
                otherByFile.get(placement.file).push(thread);
            }
        }

        for (const [file, threads] of [...otherByFile.entries()].sort()) {
            const heading = document.createElement('h3');
            heading.className = 'other-comments-file';
            heading.textContent = file;
            otherList.appendChild(heading);
            threads.forEach((thread) => otherList.appendChild(createThread(thread, true)));
        }
        otherSection.hidden = otherByFile.size === 0;
        generalSection.hidden =
            !generalList.hasChildNodes() && !generalSection.querySelector('.general-comment-new').hasChildNodes();
        renderThreadNav();
    }

    function sectionFor(filePath) {
        return [...document.querySelectorAll('.diff-file')].find((s) => s.dataset.path === filePath) || null;
    }

    // threadsCellFor returns the cell holding the threads of a line, creating its row when needed.
    function threadsCellFor(section, line) {
        const row = section.querySelector(`tr.diff-row[data-new-line="${line}"]`);
        let threadRow = row.nextElementSibling;
        if (!threadRow || !threadRow.classList.contains('diff-thread-row')) {
            threadRow = document.createElement('tr');
            threadRow.className = 'diff-thread-row';
            const cell = document.createElement('td');
            cell.colSpan = 4;
            threadRow.appendChild(cell);
            row.after(threadRow);
        }
        // Reveal the line if it sits in a folded gap
        const body = row.closest('tbody');
        if (body.hidden) {
            body.hidden = false;
            const toggle = body.previousElementSibling;
            if (toggle && toggle.classList.contains('diff-gap-toggle')) {
                toggle.remove();
            }
        }
        return threadRow.firstElementChild;
    }

    function groupThreads() {
        const threads = new Map();
        comments.filter((c) => !c.root_id).forEach((c) => threads.set(c.id, { root: c, replies: [] }));
        comments
            .filter((c) => c.root_id && threads.has(c.root_id))
            .forEach((c) => threads.get(c.root_id).replies.push(c));
        return [...threads.values()].sort((a, b) => (a.root.line_start || 0) - (b.root.line_start || 0));
    }

    function createThread(thread, showQuote) {
        const root = thread.root;
        const messages = [root, ...thread.replies];
        const latest = messages[messages.length - 1];

        const container = document.createElement('div');
        container.className = 'diff-thread';
        container.dataset.rootId = root.id;

        if (showQuote && root.selected_text) {
            const quote = document.createElement('div');
            quote.className = 'thread-item-text';
            const where = root.line_start
                ? `Line ${root.line_start}${root.line_end !== root.line_start ? `–${root.line_end}` : ''}: `
                : '';
            const outdated = root.outdated ? ' (outdated: these lines changed since)' : '';
            quote.textContent = `${where}"${root.selected_text}"${outdated}`;
            container.appendChild(quote);
        }

        for (const message of messages) {
            container.appendChild(
                createMessage(
                    message,
                    message === latest && message.author === 'user',
                    root,
                    thread.replies.length === 0,
                ),
            );
        }

        const actions = document.createElement('div');
        actions.className = 'diff-thread-actions';
        actions.appendChild(
            button('Reply', 'comment-badge-reply', () => {
                showForm(container, '', 'Reply', async (text) => {
                    const saved = await api('POST', '/api/comments', {
                        project_directory: projectDir,
                        file_path: root.file_path,
                        comment_text: text,
                        root_id: root.id,
                        author: 'user',
                    });
                    comments.push(saved);
                });
            }),
        );
        actions.appendChild(
            button('Resolve', 'comment-badge-resolve', async () => {
                if (!confirm('Are you sure you want to resolve this thread?')) return;
                if (await mutate(() => api('PATCH', `/api/comments/${root.id}/resolve`, {}))) {
                    comments = comments.filter((c) => c.id !== root.id && c.root_id !== root.id);
                    renderThreads();
                }
            }),
        );
        container.appendChild(actions);
        return container;
    }

    function createMessage(message, editable, root, rootAlone) {
        const item = document.createElement('div');
        item.className = message.root_id ? 'thread-item comment-reply' : 'thread-item comment-root';

        const author = document.createElement('div');
        author.className = 'comment-author';
        if (message.author === 'agent') {
            author.classList.add('author-agent');
        }
        const info = document.createElement('div');
        info.className = 'comment-author-info';
        const name = document.createElement('span');
        name.textContent = capitalizeFirst(message.author);
        info.appendChild(name);
        if (message.created_at) {
            const time = document.createElement('span');
            time.className = 'comment-timestamp';
            time.textContent = formatRelativeTime(message.created_at);
            info.appendChild(time);
        }
        author.appendChild(info);

        const badges = document.createElement('div');
        badges.className = 'comment-badges';
        if (editable) {
            const edit = button('Edit', 'comment-badge-edit', () => {
                showForm(item, message.comment_text, 'Save', async (text) => {
                    const updated = await api('PATCH', `/api/comments/${message.id}`, { comment_text: text });
                    comments = comments.map((c) => (c.id === message.id ? { ...updated, claimed: c.claimed } : c));
                });
            });
            if (root.claimed) {
                edit.disabled = true;
                edit.title = 'Claude is processing this thread';
            }
            badges.appendChild(edit);
        }
        if (editable && message === root && rootAlone) {
            const del = button('Delete', 'comment-badge-delete', async () => {
                if (!confirm('Are you sure you want to delete this comment?')) return;
                if (await mutate(() => api('DELETE', `/api/comments/${message.id}`))) {
                    comments = comments.filter((c) => c.id !== message.id);
                    renderThreads();
                }
            });
            del.disabled = Boolean(root.claimed);
            badges.appendChild(del);
        }
        author.appendChild(badges);
        item.appendChild(author);

        const body = document.createElement('div');
        body.className = 'thread-item-comment';
        body.innerHTML = message.rendered_html;
        item.appendChild(body);
        return item;
    }

    function button(label, className, onClick) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = `comment-badge-btn ${className}`;
        b.textContent = label;
        b.addEventListener('click', (e) => {
            e.stopPropagation();
            onClick();
        });
        return b;
    }

    // showForm appends a text form to container; onSubmit saves the text and the threads re-render.
    function showForm(container, initialText, submitLabel, onSubmit) {
        const existing = container.querySelector(':scope > .diff-thread-form');
        if (existing) {
            existing.querySelector('textarea').focus();
            return;
        }

        const form = document.createElement('form');
        form.className = 'diff-thread-form';
        const textarea = document.createElement('textarea');
        textarea.value = initialText;
        textarea.rows = 3;
        form.appendChild(textarea);

        const buttons = document.createElement('div');
        buttons.className = 'comment-popup-buttons';
        const cancel = document.createElement('button');
        cancel.type = 'button';
        cancel.className = 'comment-btn';
        cancel.textContent = 'Cancel';
        const submit = document.createElement('button');
        submit.type = 'submit';
        submit.className = 'comment-btn comment-btn-primary';
        submit.textContent = submitLabel;
        buttons.append(cancel, submit);
        form.appendChild(buttons);

        const close = () => {
            form.remove();
            renderThreads();
            reloadIfPending();
        };
        cancel.addEventListener('click', close);
        form.addEventListener('submit', async (e) => {
            e.preventDefault();
            const text = textarea.value.trim();
            if (!text) return;
            submit.disabled = true;
            if (await mutate(() => onSubmit(text))) {
                close();
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

        container.appendChild(form);
        textarea.focus();
    }

    // mutate runs an API change, reporting failures, and returns whether it succeeded.
    async function mutate(fn) {
        try {
            await fn();
            return true;
        } catch (error) {
            if (error.status === 409) {
                alert('Claude is processing this thread; try again once it has replied');
            } else {
                console.error('Request failed:', error);
                alert(`Request failed: ${error.message}`);
            }
            return false;
        }
    }

    async function api(method, url, body) {
        const options = { method };
        if (body !== undefined) {
            options.headers = { 'Content-Type': 'application/json' };
            options.body = JSON.stringify(body);
        }
        const response = await fetch(url, options);
        if (!response.ok) {
            const error = new Error((await response.text()) || `HTTP ${response.status}`);
            error.status = response.status;
            throw error;
        }
        const text = await response.text();
        return text ? JSON.parse(text) : null;
    }

    // Live updates

    function setupSSE() {
        const params = new URLSearchParams({ project_directory: projectDir, scope: 'project' });
        watchPaths.forEach((path) => params.append('watch', path));
        const eventSource = new EventSource(`/api/events?${params}`);

        ['file_updated', 'comments_resolved', 'threads_claimed', 'reload'].forEach((event) => {
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
