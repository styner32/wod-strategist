import React from 'react';
import { render } from '@testing-library/react-native';
import { AppleAiFeedbackCard, parseAppleAiFeedback } from '../ui/AppleAiFeedbackCard';

jest.mock('../../i18n', () => ({
  t: (key: string) => key,
  useLocale: () => 'ko',
}));

describe('AppleAiFeedbackCard', () => {
  describe('parseAppleAiFeedback', () => {
    it('extracts posture_feedback from a JSON string', () => {
      const json = JSON.stringify({
        appearanceHints: 'wearing athletic clothing',
        posture_feedback: 'Keep your elbows high and chest lifted.',
      });
      expect(parseAppleAiFeedback(json)).toBe('Keep your elbows high and chest lifted.');
    });

    it('extracts feedback from a JSON string when posture_feedback is absent', () => {
      const json = JSON.stringify({
        feedback: 'Maintain steady stance throughout the movement.',
      });
      expect(parseAppleAiFeedback(json)).toBe('Maintain steady stance throughout the movement.');
    });

    it('returns raw string when text is plain string', () => {
      const plain = 'Great depth on the squat!';
      expect(parseAppleAiFeedback(plain)).toBe('Great depth on the squat!');
    });

    it('falls back to raw string on malformed JSON', () => {
      const malformed = '{ invalid: json ';
      expect(parseAppleAiFeedback(malformed)).toBe(malformed);
    });
  });

  describe('component rendering', () => {
    it('renders title and status badge', () => {
      const { getByText } = render(
        <AppleAiFeedbackCard status="waiting" error={null} result={null} />
      );
      expect(getByText('appleAi.title')).toBeTruthy();
      expect(getByText('appleAi.status.waiting')).toBeTruthy();
    });

    it('renders parsed feedback and timing when result is present', () => {
      const result = {
        feedback: JSON.stringify({ posture_feedback: 'Visible posture looks stable.' }),
        capturedAt: 1727600000000,
        lastCapturedAt: 1727600005000,
        elapsedMs: 2900,
      };
      const { getByText } = render(
        <AppleAiFeedbackCard status="waiting" error={null} result={result} />
      );
      expect(getByText('Visible posture looks stable.')).toBeTruthy();
    });

    it('renders error message when in error status', () => {
      const { getByText } = render(
        <AppleAiFeedbackCard status="error" error="timeout" result={null} />
      );
      expect(getByText('appleAi.errors.timeout')).toBeTruthy();
    });
  });
});
