/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./web/templates/**/*.html.tmpl"],
  plugins: [require("daisyui")],
  daisyui: {
    themes: true,
  },
};
