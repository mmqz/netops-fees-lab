/**
 * 三步向导进度指示器：已完成（实心对勾）/ 当前（描边高亮）/ 未到达（灰）。
 */

import { IconCheck } from './icons';
import { STEPPER_LABEL } from '../i18n';

export interface StepDef {
  title: string;
}

interface StepperProps {
  steps: StepDef[];
  /** 当前步骤（0 基）。 */
  current: number;
}

export function Stepper({ steps, current }: StepperProps) {
  return (
    <ol className="stepper" aria-label={STEPPER_LABEL}>
      {steps.map((step, index) => {
        const state = index < current ? 'done' : index === current ? 'current' : 'pending';
        return (
          <li
            key={step.title}
            className={`step step--${state}`}
            aria-current={index === current ? 'step' : undefined}
          >
            {index > 0 && <span className="stepper-line" aria-hidden="true" />}
            <span className="step-circle">
              {state === 'done' ? <IconCheck size={14} /> : index + 1}
            </span>
            <span className="step-title">{step.title}</span>
          </li>
        );
      })}
    </ol>
  );
}
