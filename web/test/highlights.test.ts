import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { HighlightEventCard } from '../src/history/components/HighlightEventCard';
import {
  formatHighlightTimestamp,
  getHighlightSeekTime,
  parseHighlightSegments,
  parseHighlightTimestamp,
} from '../src/history/highlights';

describe('highlight parsing', () => {
  it('normalizes canonical highlights and fractional timestamps', () => {
    expect(parseHighlightSegments(JSON.stringify([
      {
        start: '04:52.125',
        end: '5:03',
        type: 'best_form',
        movement: 'Power Snatch',
        reason: 'Stable receiving position',
      },
    ]))).toEqual([
      {
        startSeconds: 292.125,
        endSeconds: 303,
        startLabel: '4:52.125',
        endLabel: '5:03',
        type: 'best_form',
        movement: 'Power Snatch',
        reason: 'Stable receiving position',
      },
    ]);
  });

  it('supports legacy numeric timestamp fields', () => {
    expect(parseHighlightSegments(JSON.stringify([
      { start_time: 1.5, end_time: 4.5, description: 'Good rep' },
      { start: 10, end: 12, type: 'key_moment' },
    ]))).toEqual([
      expect.objectContaining({
        startSeconds: 1.5,
        endSeconds: 4.5,
        type: 'highlight',
        reason: 'Good rep',
      }),
      expect.objectContaining({
        startSeconds: 10,
        endSeconds: 12,
        type: 'key_moment',
      }),
    ]);
  });

  it('preserves one v2 parent event with key tags and exact observations', () => {
    const result = parseHighlightSegments(JSON.stringify([
      {
        version: 2,
        start: '12:24.6',
        end: '12:29.6',
        type: 'mixed_form',
        movement: 'Deadlift',
        reason: 'Strong setup followed by an early hip rise.',
        tags: ['key_moment', 'ignored_tag'],
        observations: [
          {
            start: '12:27',
            end: '12:27.2',
            type: 'form_issue',
            reason: 'Hips rise before the shoulders.',
            confidence: 0.86,
            verified: true,
          },
          {
            start: '12:25',
            end: '12:26.4',
            type: 'positive_form',
            reason: 'Balanced setup.',
            confidence: 0.91,
          },
        ],
      },
    ]));

    expect(result).toHaveLength(1);
    expect(result[0]).toEqual(expect.objectContaining({
      version: 2,
      startSeconds: 744.6,
      endSeconds: 749.6,
      type: 'mixed_form',
      tags: ['key_moment'],
    }));
    expect(result[0].observations).toEqual([
      expect.objectContaining({
        startSeconds: 745,
        endSeconds: 746.4,
        type: 'positive_form',
      }),
      expect.objectContaining({
        startSeconds: 747,
        endSeconds: 747.2,
        startLabel: '12:27',
        endLabel: '12:27.2',
        type: 'form_issue',
        confidence: 0.86,
        verified: true,
      }),
    ]);
  });

  it('drops invalid v2 observations without duplicating the parent card', () => {
    const result = parseHighlightSegments([
      {
        version: 2,
        start: 100,
        end: 106,
        type: 'best_form',
        observations: [
          { start: 99, end: 101, type: 'positive_form' },
          { start: 100, end: 101, type: 'positive_form' },
          { start: 101, end: 102, type: 'form_issue' },
          { start: 102, end: 103, type: 'fatigue_onset' },
          { start: 103, end: 104, type: 'positive_form' },
          { start: 104, end: 103, type: 'form_issue' },
          { start: 104, end: 105, type: 'unknown' },
        ],
      },
    ]);

    expect(result).toHaveLength(1);
    expect(result[0].observations).toEqual([
      expect.objectContaining({ startSeconds: 100, endSeconds: 101 }),
      expect.objectContaining({ startSeconds: 101, endSeconds: 102 }),
      expect.objectContaining({ startSeconds: 102, endSeconds: 103 }),
    ]);
  });

  it('keeps category-representative evidence when a parent has more than three observations', () => {
    const [segment] = parseHighlightSegments([{
      version: 2,
      start: 0,
      end: 10,
      type: 'mixed_form',
      observations: [
        { start: 1, end: 2, type: 'positive_form', reason: 'positive one' },
        { start: 2, end: 3, type: 'positive_form', reason: 'positive two' },
        { start: 3, end: 4, type: 'positive_form', reason: 'positive three' },
        { start: 4, end: 5, type: 'form_issue', reason: 'only issue' },
      ],
    }]);

    expect(segment.observations).toHaveLength(3);
    expect(segment.observations?.map((observation) => observation.type)).toContain('form_issue');
    expect(segment.observations?.map((observation) => observation.reason)).toContain('only issue');
  });

  it('drops malformed, negative, and non-positive intervals', () => {
    expect(parseHighlightSegments('')).toEqual([]);
    expect(parseHighlightSegments('[]')).toEqual([]);
    expect(parseHighlightSegments('{not json')).toEqual([]);
    expect(parseHighlightSegments(JSON.stringify([
      { start: '-1', end: '0:02' },
      { start: '0:05', end: '0:05' },
      { start: '0:10', end: '0:09' },
      { start: '0:61', end: '1:05' },
      { start: '1.5:05', end: '2:00' },
      null,
    ]))).toEqual([]);
  });

  it('parses long clocks and formats exact seconds', () => {
    expect(parseHighlightTimestamp('1:02:03.5')).toBe(3723.5);
    expect(formatHighlightTimestamp(3723.5)).toBe('1:02:03.5');
  });

  it('applies legacy lead-in but starts v2 events at their padded parent boundary', () => {
    expect(getHighlightSeekTime(12.5)).toBe(7.5);
    expect(getHighlightSeekTime(3)).toBe(0);
    expect(getHighlightSeekTime(30, 20)).toBe(20);
    expect(getHighlightSeekTime(12.5, undefined, 2)).toBe(12.5);
  });

  it('renders one mixed parent card with a key tag and exact observations', () => {
    const [highlight] = parseHighlightSegments([{
      version: 2,
      start: '12:24.6',
      end: '12:29.6',
      type: 'mixed_form',
      movement: 'Deadlift',
      reason: 'Mixed evidence',
      tags: ['key_moment'],
      observations: [
        {
          start: '12:25',
          end: '12:26.4',
          type: 'positive_form',
          reason: 'Balanced setup.',
        },
        {
          start: '12:27',
          end: '12:27.2',
          type: 'form_issue',
          reason: 'Hips rise early.',
          confidence: 0.86,
        },
      ],
    }]);

    const markup = renderToStaticMarkup(createElement(HighlightEventCard, {
      highlight,
      isActive: false,
      disabled: false,
      onSelect: () => undefined,
    }));

    expect(markup.match(/<button/g)).toHaveLength(1);
    expect(markup).toContain('Mixed form');
    expect(markup).toContain('data-highlight-tag="key_moment"');
    expect(markup).toContain('Balanced setup.');
    expect(markup).toContain('Hips rise early.');
    expect(markup).toContain('12:27–12:27.2');
    expect(markup).toContain('86% confidence');
  });

  it('does not render a duplicate key tag for a standalone key event', () => {
    const [highlight] = parseHighlightSegments([{
      version: 2,
      start: 20,
      end: 25,
      type: 'key_moment',
      movement: 'Clean',
      tags: ['key_moment'],
      observations: [{
        start: 21,
        end: 22,
        type: 'technique_event',
        reason: 'Bar transition',
      }],
    }]);

    const markup = renderToStaticMarkup(createElement(HighlightEventCard, {
      highlight,
      isActive: false,
      disabled: false,
      onSelect: () => undefined,
    }));

    expect(markup).not.toContain('data-highlight-tag="key_moment"');
    expect(markup).toContain('Bar transition');
  });
});
