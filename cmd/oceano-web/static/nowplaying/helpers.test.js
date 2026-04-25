const test = require('node:test');
const assert = require('node:assert/strict');

const {
  parseVinylTrackRef,
  formatMS,
  computeElapsedMS,
} = require('./nowplaying_helpers.js');

test('parseVinylTrackRef accepts letter-number forms', () => {
  assert.deepEqual(parseVinylTrackRef('A1'), { side: 'A', track: '1' });
  assert.deepEqual(parseVinylTrackRef('b2'), { side: 'B', track: '2' });
  assert.deepEqual(parseVinylTrackRef('C-3'), { side: 'C', track: '3' });
  assert.deepEqual(parseVinylTrackRef('d.4'), { side: 'D', track: '4' });
});

test('parseVinylTrackRef accepts number-letter forms', () => {
  assert.deepEqual(parseVinylTrackRef('1A'), { side: 'A', track: '1' });
  assert.deepEqual(parseVinylTrackRef('2b'), { side: 'B', track: '2' });
  assert.deepEqual(parseVinylTrackRef('3-C'), { side: 'C', track: '3' });
  assert.deepEqual(parseVinylTrackRef('4.d'), { side: 'D', track: '4' });
});

test('parseVinylTrackRef returns null for unsupported values', () => {
  assert.equal(parseVinylTrackRef(''), null);
  assert.equal(parseVinylTrackRef('E1'), null);
  assert.equal(parseVinylTrackRef('AA'), null);
  assert.equal(parseVinylTrackRef('track 2'), null);
});

test('formatMS formats minute and hour ranges', () => {
  assert.equal(formatMS(0), '0:00');
  assert.equal(formatMS(65_000), '1:05');
  assert.equal(formatMS(3_661_000), '1:01:01');
});

test('computeElapsedMS uses seek_ms when paused or timestamp missing', () => {
  assert.equal(computeElapsedMS({ seek_ms: 12_000 }, false, 0), 12_000);
  assert.equal(computeElapsedMS({ seek_ms: 7_000, seek_updated_at: 'bad' }, true, 0), 7_000);
});

test('computeElapsedMS adds drift while playing and never goes backwards', () => {
  const updated = '2026-04-06T15:00:00.000Z';
  const now = Date.parse('2026-04-06T15:00:05.500Z');
  assert.equal(computeElapsedMS({ seek_ms: 10_000, seek_updated_at: updated }, true, now), 15_500);

  const beforeUpdate = Date.parse('2026-04-06T14:59:59.000Z');
  assert.equal(computeElapsedMS({ seek_ms: 10_000, seek_updated_at: updated }, true, beforeUpdate), 10_000);
});
