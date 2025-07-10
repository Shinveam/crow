"""
作用：模拟终端语音请求
注意：
1. 当前脚本为流式语音测试脚本，仅支持 wav 的音频输入作为 ASR 使用，wav 的采样率为16000.
2. TTS 输出音频格式仅支持 pcm，pcm 音频采样率为 16000.
"""
import base64
import json
import _thread as thread
import threading
import pyaudio    # pip install pyaudio
import websocket  # pip install websocket-client

chat_content: str = ''

hello_received = threading.Event()
stop_event = threading.Event()

p = pyaudio.PyAudio()
stream = p.open(format=pyaudio.paInt16, channels=1, rate=16000, output=True, frames_per_buffer=4096)


# 发送数据
def on_open(ws):
    def run():
        send_hello(ws)

        chunk = 1024  # 每个缓冲区的帧数
        audio_format = pyaudio.paInt16  # 采样位数
        channels = 1  # 声道数，1：单声道，2：双声道
        rate = 16000  # 采样率
        record_seconds = 600  # 录制时间 10分钟

        # 等待hello消息通知
        hello_received.wait()
        print("receive hello message")

        cur_stream = p.open(
            format=audio_format,
            channels=channels,
            rate=rate,
            input=True,
            frames_per_buffer=chunk
        )
        print("\033[91m【NOTICE】录音开始，请说话！！！\033[0m")

        for i in range(0, int(rate / chunk * record_seconds)):
            data = cur_stream.read(chunk)
            ws.send(data, opcode=websocket.ABNF.OPCODE_BINARY)

        cur_stream.stop_stream()
        cur_stream.close()

    thread.start_new_thread(run, ())


# 接收数据
def on_message(ws, message):
    global chat_content
    try:
        msg = json.loads(message)
        # print(json.dumps(msg, ensure_ascii=False, indent=4))

        match msg['type']:
            case 'hello':
                print(json.dumps(msg, ensure_ascii=False, indent=4))
                hello_received.set()

            case 'asr':
                if chat_content != '':
                    chat_content = ''
                    print('\n')
                print(f"\033[2K\r【User】: {msg['result']}", end='', flush=True)
                if msg['state'] != 0:
                    print('\n')

            case 'chat':
                chat_content += msg['text']
                print(f"\033[2K\r【Crow】: {chat_content}", end='', flush=True)

            case 'tts':
                audio = msg['audio']
                if audio != '':
                    audio_data = base64.b64decode(audio)
                    stream.write(audio_data)

    except Exception as e:
        print('【ERROR】receive msg, but parse exception:', e)


# 收到websocket错误的处理
def on_error(ws, error):
    print("\n【ERROR】", error)


# 收到websocket关闭的处理
def on_close(ws, close_status_code, close_msg):
    print("\n【CLOSED】status_code: {}, msg: {}".format(close_status_code, close_msg))
    stop_event.set() # 通知所有线程退出


def send_hello(ws):
    hello_msg = {
        'type': 'hello',
        'enable_asr': True,
        'enable_tts': True,
        'asr_params': {
            'format': 'wav',
            'sample_rate': 16000,
            'channels': 1,
            'vad_eos': 800,
            'enable_punc': True,
            'language': 'zh',
            'accent': 'mandarin'
        },
        'tts_params': {
            'speaker': 'longlaotie_v2', # longxiaochun_v2
            'format': 'pcm',
            'speed': 1.0,
            'volume': 50,
            'pitch': 1.0,
            'sample_rate': 16000,
            'language': 'zh'
        }
    }
    ws.send(json.dumps(hello_msg))


def command_line_listener(ws, stop_event):
    while not stop_event.is_set():
        try:
            user_input = input("")  # 等待命令行输入
            if user_input.strip() == 'abort':
                abort_msg = {
                    'type': 'abort'
                }
                ws.send(json.dumps(abort_msg))
                print("Abort command sent.")
            elif user_input.strip() == 'exit':
                stop_event.set()
                ws.close()  # 关闭WebSocket连接
        except Exception as e:
            break


if __name__ == '__main__':
    url = 'ws://127.0.0.1:28080/crow/v1'

    websocket.enableTrace(False)
    ws = websocket.WebSocketApp(
        url,
        on_open=on_open,
        on_message=on_message,
        on_error=on_error,
        on_close=on_close,
    )

    # 创建命令行监听线程
    command_thread = threading.Thread(
        target=command_line_listener,
        args=(ws, stop_event),
        daemon=True
    )
    command_thread.start()

    try:
        ws.run_forever()

        stream.stop_stream()
        stream.close()
        p.terminate()
    finally:
        stop_event.set()
