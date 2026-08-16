const state = {
  groups: [],
  feeds: [],
  articles: [],
  selectedGroupId: null,
  selectedFeedId: null,
  selectedArticleIndex: 0,
  modal: null,
};

async function fetchJson(url) {
  const res = await fetch(url, { credentials: 'same-origin' });
  if (!res.ok) {
    throw new Error('Request failed');
  }
  return res.json();
}

async function loadGroups() {
  const data = await fetchJson('/api/groups');
  state.groups = Array.isArray(data) ? data : [];
  if (!state.selectedGroupId && state.groups.length > 0) {
    state.selectedGroupId = state.groups[0].id;
  }
  renderGroups();
}

async function loadFeeds() {
  const data = await fetchJson('/api/feeds');
  state.feeds = Array.isArray(data) ? data : [];
  const filtered = state.selectedGroupId
    ? state.feeds.filter(feed => feed.group_id === state.selectedGroupId)
    : state.feeds;
  if (!state.selectedFeedId && filtered.length > 0) {
    state.selectedFeedId = filtered[0].id;
  }
  renderFeeds();
  if (state.selectedFeedId) {
    loadArticles(state.selectedFeedId);
  }
}

async function loadArticles(feedId) {
  if (!feedId) return;
  const data = await fetchJson(`/api/articles?feed_id=${encodeURIComponent(feedId)}`);
  state.articles = Array.isArray(data) ? data : [];
  state.selectedArticleIndex = 0;
  renderArticles();
}

function renderGroups() {
  const list = document.getElementById('group-list');
  list.innerHTML = '';
  for (const group of state.groups) {
    const item = document.createElement('button');
    item.className = 'group-item' + (group.id === state.selectedGroupId ? ' active' : '');
    item.textContent = `${group.name} (${group.feed_count || group.FeedCount || 0})`;
    item.onclick = () => {
      state.selectedGroupId = group.id;
      const filtered = state.feeds.filter(feed => feed.group_id === state.selectedGroupId);
      state.selectedFeedId = filtered[0]?.id || null;
      renderGroups();
      renderFeeds();
      if (state.selectedFeedId) loadArticles(state.selectedFeedId);
    };
    list.appendChild(item);
  }
}

function renderFeeds() {
  const list = document.getElementById('feed-list');
  list.innerHTML = '';
  const filtered = state.selectedGroupId
    ? state.feeds.filter(feed => feed.group_id === state.selectedGroupId)
    : state.feeds;
  for (const feed of filtered) {
    const item = document.createElement('button');
    item.className = 'feed-item' + (feed.id === state.selectedFeedId ? ' active' : '');
    item.textContent = feed.title;
    item.onclick = () => {
      state.selectedFeedId = feed.id;
      renderFeeds();
      loadArticles(feed.id);
    };
    list.appendChild(item);
  }
}

function renderArticles() {
  const pane = document.getElementById('article-pane');
  const header = document.getElementById('feed-header');
  const feed = state.feeds.find(item => item.id === state.selectedFeedId);
  if (!feed) {
    pane.innerHTML = '<div class="article-card">No feed selected.</div>';
    header.textContent = 'Select a feed';
    return;
  }
  header.textContent = `${feed.title}`;

  if (!state.articles.length) {
    pane.innerHTML = '<div class="article-card">No articles yet.</div>';
    return;
  }

  const article = state.articles[state.selectedArticleIndex] || state.articles[0];
  const safeBody = (article.content || article.description || '').replace(/<img[^>]*>/gi, '');
  pane.innerHTML = `
    <article class="article-card">
      <div class="meta">${article.feed_title || feed.title} • ${new Date(article.published_at || Date.now()).toLocaleString()}</div>
      <h2>${article.title}</h2>
      <div class="article-links">
        ${article.link ? `<a href="${article.link}" target="_blank" rel="noreferrer">Open article</a>` : ''}
        ${article.comments_link ? `<a href="${article.comments_link}" target="_blank" rel="noreferrer">Comments</a>` : ''}
      </div>
      <div class="article-body">${safeBody || '<p>No article preview available.</p>'}</div>
      ${article.media_url ? `<video controls src="${article.media_url}"></video>` : ''}
    </article>
  `;
}

function openAddFeedModal() {
  const dialog = document.getElementById('feed-modal');
  dialog.showModal();
}

async function saveFeed() {
  const payload = {
    url: document.getElementById('feed-url').value,
    group: document.getElementById('feed-group').value,
    display_mode: document.getElementById('feed-display-mode').value,
    sort_direction: document.getElementById('feed-sort-direction').value,
  };

  const form = new URLSearchParams();
  Object.entries(payload).forEach(([key, value]) => form.append(key, value));

  const res = await fetch('/feed/add', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: form.toString(),
    credentials: 'same-origin',
  });

  if (res.ok) {
    document.getElementById('feed-modal').close();
    await loadGroups();
    await loadFeeds();
  }
}

function bindKeyboard() {
  document.addEventListener('keydown', (event) => {
    const key = event.key.toLowerCase();
    if (event.target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(event.target.tagName)) {
      return;
    }
    if (key === 'j') {
      if (state.articles.length) {
        state.selectedArticleIndex = Math.min(state.selectedArticleIndex + 1, state.articles.length - 1);
        renderArticles();
      }
    }
    if (key === 'k') {
      if (state.articles.length) {
        state.selectedArticleIndex = Math.max(state.selectedArticleIndex - 1, 0);
        renderArticles();
      }
    }
    if (key === 'v') {
      const article = state.articles[state.selectedArticleIndex];
      if (article && article.link) {
        window.open(article.link, '_blank', 'noopener');
      }
    }
    if (key === 'c') {
      const article = state.articles[state.selectedArticleIndex];
      if (article && article.comments_link) {
        window.open(article.comments_link, '_blank', 'noopener');
      }
    }
    if (key === 'j' && event.shiftKey) {
      // next group placeholder
    }
    if (key === 'k' && event.shiftKey) {
      // prev group placeholder
    }
  });
}

document.addEventListener('DOMContentLoaded', async () => {
  document.getElementById('add-feed-btn').addEventListener('click', openAddFeedModal);
  document.getElementById('save-feed-btn').addEventListener('click', saveFeed);
  document.getElementById('cancel-feed-btn').addEventListener('click', () => document.getElementById('feed-modal').close());
  bindKeyboard();

  try {
    await loadGroups();
    await loadFeeds();
  } catch (e) {
    console.error(e);
  }
});
