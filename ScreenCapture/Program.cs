using System;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Runtime.InteropServices;

namespace ScreenCapture
{
	internal class Program
	{
		static void Main(string[] args)
		{
			File.WriteAllText("d:\\temp\\log.txt", "开始截图...\n");
			var baseDirectory = AppContext.BaseDirectory;
			Directory.SetCurrentDirectory(baseDirectory);

			int screenWidth = GetSystemMetrics(SM_CXSCREEN);
			int screenHeight = GetSystemMetrics(SM_CYSCREEN);

			IntPtr hdcScreen = GetDC(IntPtr.Zero);
			IntPtr hdcMem = CreateCompatibleDC(hdcScreen);
			IntPtr hBitmap = CreateCompatibleBitmap(hdcScreen, screenWidth, screenHeight);

			SelectObject(hdcMem, hBitmap);
			BitBlt(hdcMem, 0, 0, screenWidth, screenHeight,
				   hdcScreen, 0, 0, SRCCOPY);
			try
			{
				using (Bitmap bmp = Bitmap.FromHbitmap(hBitmap))
				{
					bmp.Save("screenshot.jpg", ImageFormat.Jpeg);
				}
			}
			finally
			{
				DeleteObject(hBitmap);
				DeleteDC(hdcMem);
				ReleaseDC(IntPtr.Zero, hdcScreen);
			}
		}

		// ====== P/Invoke 声明 ======
		const int SM_CXSCREEN = 0;
		const int SM_CYSCREEN = 1;
		const uint SRCCOPY = 0x00CC0020;

		[DllImport("user32.dll")]
		static extern int GetSystemMetrics(int nIndex);

		[DllImport("user32.dll")]
		static extern IntPtr GetDC(IntPtr hWnd);

		[DllImport("user32.dll")]
		static extern int ReleaseDC(IntPtr hWnd, IntPtr hDC);

		[DllImport("gdi32.dll")]
		static extern IntPtr CreateCompatibleDC(IntPtr hdc);

		[DllImport("gdi32.dll")]
		static extern IntPtr CreateCompatibleBitmap(IntPtr hdc, int cx, int cy);

		[DllImport("gdi32.dll")]
		static extern IntPtr SelectObject(IntPtr hdc, IntPtr h);

		[DllImport("gdi32.dll")]
		static extern bool BitBlt(IntPtr hdcDest, int xDest, int yDest, int w, int h,
								  IntPtr hdcSrc, int xSrc, int ySrc, uint rop);

		[DllImport("gdi32.dll")]
		static extern bool DeleteObject(IntPtr ho);

		[DllImport("gdi32.dll")]
		static extern bool DeleteDC(IntPtr hdc);
	}
}
