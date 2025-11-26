"""
作用：模拟终端语音请求
注意：
1. 当前脚本为流式语音测试脚本，仅支持 wav 的音频输入作为 ASR 使用，wav 的采样率为16000.
2. TTS 输出音频格式仅支持 pcm，pcm 音频采样率为 16000，具体可根据第三方厂商更改采样率.
"""
import base64
import json
import time
import _thread as thread
import threading
import pyaudio    # pip install pyaudio
import websocket  # pip install websocket-client

chat_content: str = ''

hello_received = threading.Event()
stop_event = threading.Event()

p = pyaudio.PyAudio()
stream = p.open(format=pyaudio.paInt16, channels=1, rate=16000, output=True, frames_per_buffer=4096)

# 添加全局变量
tts_playing = False
audio_buffer = bytearray()  # 用于缓存TTS音频数据（可变字节序列）
stream_lock = threading.Lock() # 保证缓冲区和音频流操作的线程安全

def reset_audio_player():
    """重置音频播放器，停止当前播放并清空缓冲区"""
    global tts_playing, audio_buffer
    with stream_lock:
        tts_playing = False
        audio_buffer.clear()  # 清空缓冲区
        # 安全地停止和重启音频流
        if stream and not stream.is_stopped():
            try:
                stream.stop_stream()
                # 短暂延迟确保驱动层清空
                time.sleep(0.05)
                # 只有在需要时才启动流
            except Exception as e:
                print(f"【DEBUG】停止流时发生异常: {e}")
    # print("\n【DEBUG】音频播放器已重置（清空缓冲区）")


def safe_stream_write(stream, data):
    """安全的流写入函数，处理各种异常情况"""
    try:
        if stream and stream.is_active():
            stream.write(data)
            return True
        else:
            return False
    except Exception as e:
        print(f"【ERROR】流写入失败: {e}")
        return False


def audio_playback_thread():
    """音频播放线程：从缓冲区读取数据并播放"""
    global tts_playing, audio_buffer

    while not stop_event.is_set():
        # 检查是否有数据需要播放
        if tts_playing and len(audio_buffer) > 0:
            with stream_lock:
                # 每次只取一部分数据，避免长时间锁定
                chunk_size = min(4096, len(audio_buffer))
                if chunk_size > 0:
                    data_to_play = bytes(audio_buffer[:chunk_size])
                    audio_buffer = audio_buffer[chunk_size:]
                else:
                    data_to_play = b""

            # 安全地播放音频数据
            if data_to_play:
                if not safe_stream_write(stream, data_to_play):
                    # 如果写入失败，尝试恢复流状态
                    try:
                        if stream and stream.is_stopped():
                            stream.start_stream()
                            # 重试播放
                            safe_stream_write(stream, data_to_play)
                    except Exception as e:
                        print(f"【ERROR】流恢复失败: {e}")
        else:
            # 无数据时短暂休眠
            time.sleep(0.002)


def recover_audio_system():
    """在严重错误时恢复整个音频系统"""
    global stream, p, audio_buffer, tts_playing

    print("【DEBUG】尝试恢复音频系统...")

    with stream_lock:
        # 重置状态
        tts_playing = False
        audio_buffer.clear()

        # 安全关闭现有资源
        if stream:
            try:
                if stream.is_active():
                    stream.stop_stream()
                stream.close()
            except:
                pass

        # 重新初始化音频系统
        try:
            p = pyaudio.PyAudio()
            stream = p.open(format=pyaudio.paInt16, channels=1, rate=16000,
                           output=True, frames_per_buffer=4096)
            print("【DEBUG】音频系统恢复成功")
        except Exception as e:
            print(f"【ERROR】音频系统恢复失败: {e}")


# 发送数据
def on_open(ws):
    def run():
        send_hello(ws)
         # 等待hello消息通知
        hello_received.wait()
        print("receive hello message")

        chunk = 1024  # 每个缓冲区的帧数
        audio_format = pyaudio.paInt16  # 采样位数
        channels = 1  # 声道数，1：单声道，2：双声道
        rate = 16000  # 采样率
        record_seconds = 600  # 录制时间 10分钟

        cur_stream = p.open(
            format=audio_format,
            channels=channels,
            rate=rate,
            input=True,
            frames_per_buffer=chunk
        )
        print("\033[91m【NOTICE】录音开始，请说话！！！\033[0m")

        for i in range(0, int(rate / chunk * record_seconds)):
            if stop_event.is_set():
                break
            data = cur_stream.read(chunk)
            ws.send(data, opcode=websocket.ABNF.OPCODE_BINARY)

        cur_stream.stop_stream()
        cur_stream.close()

    thread.start_new_thread(run, ())


# 接收数据
def on_message(ws, message):
    global chat_content, tts_playing, audio_buffer
    try:
        msg = json.loads(message)
        # print(json.dumps(msg, ensure_ascii=False, indent=4))

        match msg['type']:
            case 'hello':
                hello_received.set()

            case 'asr':
                # 收到ASR时重置播放器
                try:
                    reset_audio_player()
                except Exception as reset_error:
                    print(f"【ERROR】重置播放器失败: {reset_error}")
                    # 尝试更彻底的恢复
                    recover_audio_system()

                if chat_content != '':
                    chat_content = ''
                    print('\n')
                print(f"\033[2K\r【User】: {msg['result']}", end='', flush=True)
                if msg['state'] != 0:
                    print('\n')

            case 'chat':
                chat_content += msg['text']
                print(f"\033[2K\r【Machine】: {chat_content}", end='', flush=True)

            case 'tts':
                state = msg.get('state', 0)
                audio = msg.get('audio', '')
                if audio:
                    try:
                        # 解码Base64音频数据
                        audio_data = base64.b64decode(audio)
                        with stream_lock:
                            # 检查播放状态，避免在重置后继续添加数据
                            if tts_playing:
                                audio_buffer.extend(audio_data)
                    except Exception as e:
                        print(f"\n【ERROR】TTS数据解码失败: {e}")

                # 处理TTS状态
                if state == 0:  # 合成进行中
                    tts_playing = True
                    # print("\n【DEBUG】TTS合成中，开始播放")
                elif state == 1:  # 合成结束
                    tts_playing = False  # 播放完缓冲区数据后停止
                    print("\n【DEBUG】TTS合成结束，等待播放完成")

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
            'speaker': '', # longlaotie_v2
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

    # 启动音频播放线程
    playback_thread = threading.Thread(target=audio_playback_thread, daemon=True)
    playback_thread.start()

    # 创建命令行监听线程
    command_thread = threading.Thread(
        target=command_line_listener,
        args=(ws, stop_event),
        daemon=True
    )
    command_thread.start()

    try:
        ws.run_forever()
    finally:
        stop_event.set()
        # 等待播放线程处理完剩余数据（最多等待1秒）
        playback_thread.join(timeout=1.0)
        # 安全释放音频资源
        with stream_lock:
            if stream.is_active():
                stream.stop_stream()
            stream.close()
        p.terminate()
        print("资源已释放，程序退出")
