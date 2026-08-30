import type { ThemeConfig } from 'antd'

export const theme: ThemeConfig = {
  token: {
    colorPrimary: '#c41d1d',
    colorInfo: '#1677ff',
    colorSuccess: '#389e0d',
    colorWarning: '#d48806',
    colorError: '#cf1322',
    borderRadius: 6,
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif",
  },
  components: {
    Layout: {
      siderBg: '#14181f',
      triggerBg: '#0f1218',
      headerBg: '#ffffff',
      bodyBg: '#f4f6f8',
    },
    Menu: {
      darkItemBg: '#14181f',
      darkSubMenuItemBg: '#14181f',
      darkItemSelectedBg: '#c41d1d',
      darkItemHoverBg: '#1f2530',
      darkItemColor: 'rgba(255,255,255,0.78)',
      darkItemSelectedColor: '#ffffff',
    },
    Table: {
      headerBg: '#fafafa',
    },
  },
}
