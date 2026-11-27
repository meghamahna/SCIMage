// Console behaviour lives here rather than inline so the Content-Security-Policy
// can refuse inline script. Markup opts in through data- attributes and ids.

// A select marked data-autosubmit submits its form as soon as it changes.
document.addEventListener('change', function (e) {
  var el = e.target;
  if (el.matches && el.matches('select[data-autosubmit]') && el.form) { el.form.submit(); }
});

// A form marked data-confirm asks first, with the text html/template escaped
// into the attribute, and submits only on OK.
document.addEventListener('submit', function (e) {
  var form = e.target;
  if (form.matches && form.matches('form[data-confirm]') && !confirm(form.dataset.confirm)) {
    e.preventDefault();
  }
});

document.addEventListener('click', function (e) {
  if (e.target.closest && e.target.closest('[data-reload]')) { location.reload(); }
});

// ARIA page.
// In-place update: the briefing is fetched in the background and only the
// #aria-brief block is swapped, so the URL stays on /console/aria (no full-page
// POST to /aria/narrate). The listener is delegated on the stable container, so
// the button inside the freshly injected fragment keeps working.
(function () {
  var container = document.getElementById('aria-brief');
  if (!container) { return; }
  container.addEventListener('submit', async function (e) {
    var form = e.target.closest('.narrate-form');
    if (!form) { return; }
    e.preventDefault();
    var btn = form.querySelector('button[type=submit]');
    var status = form.querySelector('.brief-status');
    var originalLabel = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = 'Generating…'; btn.classList.add('busy'); }
    if (status) { status.hidden = false; }
    try {
      var res = await fetch(form.action, {
        method: 'POST',
        headers: { 'X-Requested-With': 'fetch', 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams(new FormData(form)).toString()
      });
      if (!res.ok) { throw new Error('status ' + res.status); }
      container.innerHTML = await res.text();
    } catch (err) {
      if (btn) { btn.disabled = false; btn.textContent = originalLabel; btn.classList.remove('busy'); }
      if (status) { status.hidden = true; }
      container.insertAdjacentHTML('afterbegin', '<div class="error">Couldn\'t reach the server just now. Try again.</div>');
    }
  });
})();

// Webhooks page.
// Progressive enhancement: with JavaScript on, Replay happens in place.
// Requeue, then poll the delivery until it lands or parks again. With it off,
// the form still POSTs and the page reloads.
(function () {
  var sleep = function (ms) { return new Promise(function (r) { setTimeout(r, ms); }); };
  var wrap = document.querySelector('[data-webhooks-on]');
  if (!wrap) { return; }
  var webhooksOn = wrap.dataset.webhooksOn === '1';

  function setCell(cell, html) { cell.innerHTML = html; }

  function spinner(label) {
    return '<span class="replay-status"><span class="spinner" aria-hidden="true"></span>' + label + '</span>';
  }

  function restoreButton(cell, id, csrf) {
    setCell(cell,
      '<form method="post" action="/console/webhooks/replay" class="form-inline replay-form" data-id="' + id + '">' +
      '<input type="hidden" name="csrf" value="' + csrf + '">' +
      '<input type="hidden" name="id" value="' + id + '">' +
      '<button type="submit" class="ghost">Replay</button></form>');
    wire(cell.querySelector('.replay-form'));
  }

  function removeRow(row) {
    row.classList.add('leaving');
    setTimeout(function () {
      var tbody = row.parentNode;
      row.remove();
      if (tbody && !tbody.querySelector('tr[data-delivery]')) {
        tbody.innerHTML = '<tr><td colspan="8" class="empty">Nothing parked or queued. Everything has been delivered.</td></tr>';
      }
      updateSelection();
    }, 400);
  }

  function setStatus(row, html) {
    var cell = row.querySelector('.status-cell');
    if (cell) { setCell(cell, html); }
  }

  function delivered(row) {
    setCell(row.querySelector('.replay-cell'), '<span class="replay-status ok">✓ Delivered</span>');
    setTimeout(function () { removeRow(row); }, 1000);
  }

  // The delivery was already requeued elsewhere (a 404 from replay), so it's
  // no longer parked, so clear it without the red failure state.
  function gone(row) {
    setCell(row.querySelector('.replay-cell'), '<span class="replay-status muted">Already handled</span>');
    setTimeout(function () { removeRow(row); }, 1200);
  }

  // Requeued and waiting: it's back on the queue as pending. This is the
  // resting state when the receiver is slow or webhooks are turned off: a
  // neutral note, never a red failure. The hint says why it hasn't delivered.
  function queued(cell, note) {
    var html = '<span class="replay-status muted">Queued</span>';
    if (note) { html += '<div class="replay-note">' + note + '</div>'; }
    setCell(cell, html);
  }

  function failed(cell, id, csrf) {
    setCell(cell, '<span class="replay-status bad">! Failed</span>');
    var row = cell.closest('tr');
    if (row) { setStatus(row, '<span class="pill warn">Parked</span>'); }
    setTimeout(function () { restoreButton(cell, id, csrf); }, 2500);
  }

  async function poll(row, cell, id, csrf, tries) {
    // The first ~24s poll quickly (1.5s) to catch the common case fast. Past
    // that, a slow receiver or a long backoff (retries can span minutes) is
    // still in progress, not abandoned, so keep watching at a gentler pace
    // instead of freezing on a stale "Queued" that never resolves.
    if (tries === 16) {
      queued(cell, 'Still waiting for the receiver to accept it.');
    }
    await sleep(tries < 16 ? 1500 : 8000);
    var status = 'pending';
    try {
      var res = await fetch('/console/webhooks/delivery-status?id=' + encodeURIComponent(id), { headers: { 'Accept': 'application/json' } });
      status = (await res.json()).status;
    } catch (e) { /* transient, keep polling */ }
    if (status === 'delivered') { delivered(row); return; }
    if (status === 'dead_letter') { failed(cell, id, csrf); return; }
    poll(row, cell, id, csrf, tries + 1);
  }

  async function replay(form) {
    var row = form.closest('tr');
    var cell = form.closest('.replay-cell');
    var id = form.dataset.id;
    var csrf = form.querySelector('input[name=csrf]').value;
    setCell(cell, spinner('Replaying…'));
    try {
      var res = await fetch(form.action, {
        method: 'POST',
        headers: { 'Accept': 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'csrf=' + encodeURIComponent(csrf) + '&id=' + encodeURIComponent(id)
      });
      if (res.status === 404) { gone(row); return; }
      if (res.status === 409) {
        // Refused, not failed: the server won't requeue a delivery nothing can
        // send. Buttons are disabled up front when webhooks are off (see
        // below), so this only fires on a stale tab left open across a config
        // change; restore the button rather than showing a red failure.
        restoreButton(cell, id, csrf);
        return;
      }
      if (!res.ok) { failed(cell, id, csrf); return; }
      setStatus(row, '<span class="pill info">Queued</span>');
      poll(row, cell, id, csrf, 0);
    } catch (e) {
      failed(cell, id, csrf);
    }
  }

  function wire(form) {
    if (!form) { return; }
    form.addEventListener('submit', function (e) { e.preventDefault(); replay(form); });
  }

  document.querySelectorAll('.replay-form').forEach(wire);

  // With webhooks off, nothing can ever deliver a replayed event: disable
  // Replay everywhere up front rather than letting an operator trigger a
  // request the server is just going to refuse (see the 409 handling above).
  if (!webhooksOn) {
    document.querySelectorAll('.replay-form button[type=submit]').forEach(function (btn) {
      btn.disabled = true;
      btn.title = 'Webhooks are off, so a replay has nothing to send it.';
    });
  }

  // Bulk replay: a checkbox per row plus a header "select all", both
  // driving the same per-row replay() so each delivery still gets its own
  // spinner, poll, and admin-audit entry. Bulk is just "click Replay on
  // several rows at once", not a separate code path.
  var selectAll = document.getElementById('replay-select-all');
  var replaySelectedBtn = document.getElementById('replay-selected');
  var replaySelectedCount = document.getElementById('replay-selected-count');

  if (!webhooksOn && replaySelectedBtn) {
    replaySelectedBtn.title = 'Webhooks are off, so a replay has nothing to send it.';
  }

  function updateSelection() {
    if (!replaySelectedBtn) { return; }
    var all = document.querySelectorAll('.replay-select');
    var checked = document.querySelectorAll('.replay-select:checked');
    replaySelectedBtn.disabled = !webhooksOn || checked.length === 0;
    replaySelectedCount.textContent = checked.length ? checked.length + ' selected' : '';
    if (selectAll) { selectAll.checked = all.length > 0 && checked.length === all.length; }
  }

  if (selectAll) {
    selectAll.addEventListener('change', function () {
      document.querySelectorAll('.replay-select').forEach(function (cb) { cb.checked = selectAll.checked; });
      updateSelection();
    });
  }

  document.querySelectorAll('.replay-select').forEach(function (cb) {
    cb.addEventListener('change', updateSelection);
  });

  if (replaySelectedBtn) {
    replaySelectedBtn.addEventListener('click', function () {
      var checked = Array.prototype.slice.call(document.querySelectorAll('.replay-select:checked'));
      checked.forEach(function (cb) {
        cb.disabled = true;
        cb.checked = false;
        var form = cb.closest('tr').querySelector('.replay-form');
        if (form) { replay(form); }
      });
      updateSelection();
    });
  }
})();
