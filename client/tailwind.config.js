/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        // Discord-inspired surface palette. Names mirror common design-system
        // tokens (bg-1 darkest, bg-3 lightest) so components don't pin to a
        // specific gray shade.
        bg: {
          1: '#0b0d11', // page background, deepest
          2: '#15171d', // primary surface (sidebar, panels)
          3: '#1c1f27', // raised surface (modals, hover)
          4: '#252934', // input bg / chip
          5: '#3a4150', // border / divider
        },
        ink: {
          1: '#f5f6f8', // strongest text
          2: '#cbd0d8', // body
          3: '#94a0b0', // muted
          4: '#5f6a78', // disabled / placeholder
        },
        brand: {
          50:  '#eef4ff',
          100: '#dbe6ff',
          200: '#bccdff',
          300: '#8eaaff',
          400: '#5d82ff',
          500: '#3a64ee', // primary
          600: '#2c4ed1',
          700: '#243eb0',
          800: '#1d3489',
          900: '#172969',
        },
        accent: {
          green: '#3ba55c',  // success / online
          red:   '#ed4245',  // danger / unread
          amber: '#faa61a',
        },
      },
      boxShadow: {
        soft: '0 1px 2px rgba(0,0,0,0.18), 0 4px 12px rgba(0,0,0,0.25)',
        pop:  '0 10px 30px rgba(0,0,0,0.45)',
      },
      borderRadius: {
        lg: '0.625rem',
        xl: '0.875rem',
      },
    },
  },
  plugins: [],
};
