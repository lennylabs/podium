// The threshold control. It is a native range input inside a pill that
// carries the label, the two ends of the range, and the current value as an
// "≥ 0.85" tag, so the reader sees what the read applies without dragging.

import { useId } from 'react';

import { formatThreshold } from '../similarity';

/** RangeSlider is the similarity threshold. The track fills to the value, and
 * the value is stated beside the track as the lower bound it sets. The
 * screen that owns the read decides what a change re-reads; the control only
 * reports the value the reader chose.
 *
 * Spec: §13.10 */
export function RangeSlider({
  label,
  min,
  max,
  step,
  value,
  onChange,
  showEnds = true,
}: {
  label: string;
  min: number;
  max: number;
  step: number;
  value: number;
  onChange: (value: number) => void;
  /** showEnds draws the two ends of the range beside the track. A compact
   * placement drops them and keeps the value tag. */
  showEnds?: boolean;
}) {
  const id = useId();
  const share = max === min ? 0 : (value - min) / (max - min);
  const fill = `${String(Math.round(Math.max(0, Math.min(1, share)) * 100))}%`;
  const shown = formatThreshold(value);
  return (
    <div className="range-slider">
      <label htmlFor={id} className="range-slider-label">
        {label}
      </label>
      {showEnds && (
        <span className="range-slider-end" aria-hidden="true">
          {formatThreshold(min).replace(/0$/, '')}
        </span>
      )}
      <input
        id={id}
        type="range"
        className="range-slider-input"
        min={min}
        max={max}
        step={step}
        value={value}
        aria-valuetext={`at least ${shown}`}
        // The fill is a share of the track, carried as a custom property so
        // the colours stay in the stylesheet's tokens.
        style={{ ['--fill' as string]: fill }}
        onChange={(event) => {
          onChange(Number(event.currentTarget.value));
        }}
      />
      {showEnds && (
        <span className="range-slider-end" aria-hidden="true">
          {formatThreshold(max).replace(/0$/, '')}
        </span>
      )}
      <output htmlFor={id} className="range-slider-value">
        ≥ {shown}
      </output>
    </div>
  );
}
