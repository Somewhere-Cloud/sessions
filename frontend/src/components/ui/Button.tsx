import { forwardRef, type ButtonHTMLAttributes } from 'react';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'secondary' | 'primary' | 'ghost' | 'danger';
  size?: 'compact' | 'default';
  /** Blocks activation and exposes busy state. Keep the visible label meaningful. */
  loading?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'secondary', size = 'default', loading = false, disabled, className = '', type = 'button', children, ...props }, ref
) {
  return <button {...props} ref={ref} type={type} disabled={disabled || loading}
    aria-busy={loading || undefined} className={`btn btn-${variant} ui-button ui-${size} ${className}`}>
    {children}
  </button>;
});

export interface IconButtonProps extends Omit<ButtonProps, 'aria-label'> {
  'aria-label': string;
}

/** The label is required even when a tooltip/title is supplied. Decorative icons stay hidden. */
export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { className = '', variant = 'ghost', children, ...props }, ref
) {
  return <Button {...props} ref={ref} variant={variant} className={`ui-icon-button ${className}`}>
    <span aria-hidden="true">{children}</span>
  </Button>;
});
